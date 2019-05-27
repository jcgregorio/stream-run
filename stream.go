package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	units "github.com/docker/go-units"
	"github.com/gorilla/mux"
	blackfriday "gopkg.in/russross/blackfriday.v2"

	"github.com/jcgregorio/go-lib/admin"
	"github.com/jcgregorio/go-lib/config"
	"github.com/jcgregorio/logger"
	"github.com/jcgregorio/stream-run/entries"
)

var (
	entryDB *entries.Entries

	log = logger.New()

	adminTemplate = template.Must(template.New("admin").Funcs(template.FuncMap{
		"trunc": func(s string) string {
			if len(s) > 80 {
				return s[:80] + "..."
			}
			return s
		},
		"humanTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return " • " + units.HumanDuration(time.Now().Sub(t)) + " ago"
		},
	}).Parse(fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title></title>
    <meta charset="utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=egde,chrome=1">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta name="google-signin-scope" content="profile email">
    <meta name="google-signin-client_id" content="%s">
    <script src="https://apis.google.com/js/platform.js" async defer></script>
		<style type="text/css" media="screen">
		  .created {
			  float: right;
			}
		</style>
</head>
<body>
  <div class="g-signin2" data-onsuccess="onSignIn" data-theme="dark"></div>
    <script>
      function onSignIn(googleUser) {
        document.cookie = "id_token=" + googleUser.getAuthResponse().id_token;
        if (!{{.IsAdmin}}) {
          window.location.reload();
        }
      };
    </script>
	<div><a href="?offset={{.Offset}}">Next</a></div>
  {{range .Entries}}
		<div>
			<h2>{{ .Title }}</h2>
			<div>
        <span class=created>{{ .Created | humanTime }}</span>
				{{ .Content }}
			</div>
		</div>
  {{end}}
	<hr>
	<div>
		<form action="/admin/new" method="post" accept-charset="utf-8">
		  <p><input type="text" name="title" value=""></p>
      <textarea name="content" rows="8" cols="80"></textarea>
			<p><input type="submit" value="Insert"></p>
		</form>
	</div>
</body>
</html>`, config.CLIENT_ID)))
)

func initialize() {
	var err error
	entryDB, err = entries.New(context.Background(), config.PROJECT, config.DATASTORE_NAMESPACE, log)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Info("Initialized.")
	}
}

type adminContext struct {
	IsAdmin bool
	Entries []*EntryContent
	Offset  int
}

type EntryContent struct {
	Title   string
	Content template.HTML
	ID      string
	Created time.Time
}

func withDefault(s, defaultValue string) string {
	if s == "" {
		return defaultValue
	}
	return s
}

// adminHandler displays the admin page for Stream.
func adminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	context := &adminContext{}
	isAdmin := admin.IsAdmin(r, log)
	if isAdmin {
		limitText := withDefault(r.FormValue("limit"), "20")
		limit, err := strconv.ParseInt(limitText, 10, 32)
		if err != nil {
			log.Infof("Failed to parse limit: %s", err)
			return
		}
		offsetText := withDefault(r.FormValue("offset"), "0")
		offset, err := strconv.ParseInt(offsetText, 10, 32)
		if err != nil {
			log.Infof("Failed to parse offset: %s", err)
			return
		}
		entries, err := entryDB.List(r.Context(), int(limit), int(offset))
		if err != nil {
			log.Warningf("Failed to get entries: %s", err)
			return
		}
		context = &adminContext{
			IsAdmin: isAdmin,
			Entries: toDisplay(entries),
			Offset:  int(offset + limit),
		}
	}
	if err := adminTemplate.Execute(w, context); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
}

func toDisplay(in []*entries.Entry) []*EntryContent {
	ret := []*EntryContent{}
	test := "title\n=====\n  * item\n  *item"
	fmt.Printf("%q %q\n", test, string(blackfriday.Run([]byte(test))))
	for _, en := range in {
		content := strings.ReplaceAll(en.Content, "\r\n", "\n")

		fmt.Printf("%q %q\n", en.Content, string(blackfriday.Run([]byte(content))))
		ret = append(ret, &EntryContent{
			Title:   en.Title,
			Content: template.HTML(blackfriday.Run([]byte(content))),
			ID:      en.ID,
			Created: en.Created,
		})
	}
	return ret
}

// adminNewHandler displays the admin page for Stream.
func adminNewHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.IsAdmin(r, log) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	_, err := entryDB.Insert(r.Context(), r.FormValue("content"), r.FormValue("title"))
	if err != nil {
		log.Errorf("Failed to insert: %s", err)
		http.Error(w, "Failed to insert", http.StatusInternalServerError)
	}
	http.Redirect(w, r, "/admin", 302)
}

func main() {
	initialize()
	/*

				/           - Root, displays the last 10 stream entries. Link to feed.
				              Link to admin page. Link to rollup page. Links to entry permalinks.
				/entry/<id> - Permalink for each entry.
				/feed       - Atom feed of last 10 stream entries.
				/admin      - Must be logged in and admin to access. Allows creating/editing/deleting stream entries.
		    /admin/entry
				            - POST to create.
		    /admin/entry/<id>
				            - GET to view and edit.
							      - PUT to update.
							      - DELETE to delete.
		    /admin/rollup
				            - A formatted post of the last N entries, used to create a rollup blog entry.

	*/

	r := mux.NewRouter()
	r.HandleFunc("/admin/new", adminNewHandler).Methods("POST", "OPTIONS")
	r.HandleFunc("/admin", adminHandler).Methods("GET", "OPTIONS")

	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":"+config.PORT, nil))
}
