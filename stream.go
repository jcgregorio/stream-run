package main

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	units "github.com/docker/go-units"
	"github.com/gorilla/mux"

	"github.com/jcgregorio/go-lib/admin"
	"github.com/jcgregorio/go-lib/config"
	"github.com/jcgregorio/logger"
	"github.com/jcgregorio/stream-run/entries"
)

var (
	e *entries.Entries

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
		  #webmentions {
				display: grid;
				padding: 1em;
				grid-template-columns: 5em 10em 1fr;
				grid-column-gap: 10px;
				grid-row-gap: 6px;
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
  {{range .Entries}}
	  <h2>{{ .Title }} <span>{{ .Created | humanTime }}</span></h2>
		<div>
		  {{ .Content }}
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
	e, err = entries.New(context.Background(), config.PROJECT, config.DATASTORE_NAMESPACE, log)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Info("Initialized.")
	}
}

type adminContext struct {
	IsAdmin bool
	Entries []*entries.Entry
}

// adminHandler displays the admin page for Webmentions.
func adminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	context := &adminContext{}
	isAdmin := admin.IsAdmin(r, log)
	if isAdmin {
		limitText := r.FormValue("limit")
		if limitText == "" {
			limitText = "20"
		}
		limit, err := strconv.ParseInt(limitText, 10, 32)
		if err != nil {
			log.Infof("Failed to parse limit: %s", err)
			return
		}
		/*
			offsetText := r.FormValue("offset")
			if offsetText == "" {
				offsetText = "0"
			}
			offset, err := strconv.ParseInt(offsetText, 10, 32)
			if err != nil {
				log.Infof("Failed to parse offset: %s", err)
				return
			}
		*/
		entries, err := e.List(r.Context(), int(limit))
		if err != nil {
			log.Warningf("Failed to get entries: %s", err)
			return
		}
		context = &adminContext{
			IsAdmin: isAdmin,
			Entries: entries,
		}
	}
	if err := adminTemplate.Execute(w, context); err != nil {
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
							      - PUT to update.
							      - DELETE to delete.
		    /admin/rollup
				            - A formatted post of the last N entries, used to create a rollup blog entry.

	*/

	r := mux.NewRouter()
	r.HandleFunc("/admin", adminHandler).Methods("GET", "OPTIONS")

	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":"+config.PORT, nil))
}
