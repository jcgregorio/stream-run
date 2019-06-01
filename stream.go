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
			.entry {
			  margin: 1em;
				padding: 1em;
				background: #eee;
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
		<div class=entry>
			<h2>{{ .Title }}</h2>
			<div>
        <span class=created>{{ .Created | humanTime }}</span>
				{{ .Content }}
			</div>
			<a href="/admin/edit/{{ .ID }}">Edit</a>
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

	adminEditTemplate = template.Must(template.New("adminEdit").Funcs(template.FuncMap{
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
	}).Parse(`<!DOCTYPE html>
<html>
<head>
    <title></title>
    <meta charset="utf-8" />
    <meta http-equiv="X-UA-Compatible" content="IE=egde,chrome=1">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
		<style type="text/css" media="screen">
		  .created {
			  float: right;
			}
			.entry {
			  margin: 1em;
				padding: 1em;
				background: #eee;
			}
		</style>
</head>
<body>
  {{with .Cooked}}
	<div class=entry>
		<h2>{{ .Title }}</h2>
		<div>
			<span class=created>{{ .Created | humanTime }}</span>
			{{ .Content }}
		</div>
	</div>
	{{end}}
	<hr>
	{{with .Raw}}
	<div>
		<form action="/admin/edit/{{ .ID }}" method="post" accept-charset="utf-8">
		  <p><input type="text" name="title" value="{{ .Title }}"></p>
      <textarea name="content" rows="8" cols="80">{{ .Content }}</textarea>
			<p><input type="submit" value="Update"></p>
		</form>
	</div>
	{{end}}
</body>
</html>`))
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

func parseWithDefault(s string, defaultValue int) int {
	// "" will parse as an error.
	ret, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return defaultValue
	}
	return int(ret)
}

// adminHandler displays the admin page for Stream.
func adminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	context := &adminContext{}
	isAdmin := admin.IsAdmin(r, log)
	if isAdmin {
		limit := parseWithDefault(r.FormValue("limit"), 20)
		offset := parseWithDefault(r.FormValue("offset"), 0)
		entries, err := entryDB.List(r.Context(), int(limit), int(offset))
		if err != nil {
			log.Warningf("Failed to get entries: %s", err)
			return
		}
		context = &adminContext{
			IsAdmin: isAdmin,
			Entries: toDisplaySlice(entries),
			Offset:  int(offset + limit),
		}
	}
	if err := adminTemplate.Execute(w, context); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
}

func toDisplay(in *entries.Entry) *EntryContent {
	content := strings.ReplaceAll(in.Content, "\r\n", "\n")
	return &EntryContent{
		Title:   in.Title,
		Content: template.HTML(blackfriday.Run([]byte(content))),
		ID:      in.ID,
		Created: in.Created,
	}
}

func toDisplaySlice(in []*entries.Entry) []*EntryContent {
	ret := []*EntryContent{}
	for _, en := range in {
		ret = append(ret, toDisplay(en))
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

type EditContext struct {
	Raw    *entries.Entry
	Cooked *EntryContent
}

// adminEditHandler displays the admin page for Stream.
func adminEditHandler(w http.ResponseWriter, r *http.Request) {
	if !admin.IsAdmin(r, log) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	vars := mux.Vars(r)
	id := vars["id"]
	raw, err := entryDB.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c := EditContext{
		Raw:    raw,
		Cooked: toDisplay(raw),
	}
	if err := adminEditTemplate.Execute(w, c); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
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
	r.HandleFunc("/admin/edit/{id}", adminEditHandler).Methods("GET", "POST", "OPTIONS")
	r.HandleFunc("/admin", adminHandler).Methods("GET", "OPTIONS")

	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":"+config.PORT, nil))
}
