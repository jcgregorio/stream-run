package main

import (
	"context"
	"flag"
	"html/template"
	"net/http"
	"path/filepath"
	"runtime"
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

// flags
var (
	local        = flag.Bool("local", false, "Running locally if true. As opposed to in production.")
	resourcesDir = flag.String("resources_dir", "", "The directory to find templates, JS, and CSS files. If blank the current directory will be used.")
)

var (
	entryDB *entries.Entries

	templates *template.Template

	log = logger.New()
)

func loadTemplates() {
	// pattern is the glob pattern used to find all the template files.
	pattern := filepath.Join(*resourcesDir, "*.html")

	templates = template.New("")
	templates.Funcs(template.FuncMap{
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
	})
	template.Must(templates.ParseGlob(pattern))
}

func initialize() {
	flag.Parse()

	if *resourcesDir == "" {
		_, filename, _, _ := runtime.Caller(0)
		*resourcesDir = filepath.Join(filepath.Dir(filename), "templates")
	}
	loadTemplates()

	var err error
	entryDB, err = entries.New(context.Background(), config.PROJECT, config.DATASTORE_NAMESPACE, log)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Info("Initialized.")
	}
}

type adminContext struct {
	IsAdmin  bool
	Entries  []*EntryContent
	Offset   int
	ClientID string
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
	context = &adminContext{
		IsAdmin:  false,
		ClientID: config.CLIENT_ID,
	}
	if isAdmin {
		limit := parseWithDefault(r.FormValue("limit"), 20)
		offset := parseWithDefault(r.FormValue("offset"), 0)
		entries, err := entryDB.List(r.Context(), int(limit), int(offset))
		if err != nil {
			log.Warningf("Failed to get entries: %s", err)
			return
		}
		context.IsAdmin = true
		context.Entries = toDisplaySlice(entries)
		context.Offset = int(offset + limit)
	}
	if err := templates.ExecuteTemplate(w, "admin.html", context); err != nil {
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
	if err := templates.ExecuteTemplate(w, "adminEdit.html", c); err != nil {
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
