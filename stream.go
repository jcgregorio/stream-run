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
	pattern := filepath.Join(*resourcesDir, "*.*ml")

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
		"atomTime": func(t time.Time) string {
			return t.Format(time.RFC3339)
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
	Title       string
	Content     template.HTML
	SafeContent string
	ID          string
	Created     time.Time
	Updated     time.Time
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
		IsAdmin:  isAdmin,
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
		context.Entries = toDisplaySlice(entries)
		context.Offset = int(offset + limit)
	}
	if err := templates.ExecuteTemplate(w, "admin.html", context); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
}

type indexContext struct {
	Entries []*EntryContent
	Offset  int
}

// indexHandler displays the admin page for Stream.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	limit := parseWithDefault(r.FormValue("limit"), 20)
	offset := parseWithDefault(r.FormValue("offset"), 0)
	entries, err := entryDB.List(r.Context(), int(limit), int(offset))
	if err != nil {
		log.Warningf("Failed to get entries: %s", err)
		return
	}
	context := &adminContext{
		Entries: toDisplaySlice(entries),
		Offset:  int(offset + limit),
	}
	if err := templates.ExecuteTemplate(w, "index.html", context); err != nil {
		log.Errorf("Failed to render index template: %s", err)
	}
}

type feedContext struct {
	Updated time.Time
	Host    string
	Entries []*EntryContent
	Author  string
}

// feedHandler displays the admin page for Stream.
func feedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	entries, err := entryDB.List(r.Context(), 10, 0)
	if err != nil {
		log.Warningf("Failed to get entries: %s", err)
		return
	}
	updated := time.Time{}
	for _, entry := range entries {
		if entry.Updated.After(updated) {
			updated = entry.Updated
		}
	}
	context := &feedContext{
		Host:    config.HOST,
		Author:  config.AUTHOR,
		Updated: updated,
		Entries: toDisplaySlice(entries),
	}
	if err := templates.ExecuteTemplate(w, "atom.xml", context); err != nil {
		log.Errorf("Failed to render index template: %s", err)
	}
}

func toDisplay(in *entries.Entry) *EntryContent {
	content := strings.ReplaceAll(in.Content, "\r\n", "\n")
	content = string(blackfriday.Run([]byte(content)))
	return &EntryContent{
		Title:       in.Title,
		Content:     template.HTML(content),
		SafeContent: content,
		ID:          in.ID,
		Created:     in.Created,
		Updated:     in.Updated,
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
	if *local {
		loadTemplates()
	}
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
	if *local {
		loadTemplates()
	}
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
	if r.Method == "POST" {
		switch r.FormValue("action") {
		case "update":
			raw.Title = r.FormValue("title")
			raw.Content = r.FormValue("content")
			if err := entryDB.Update(r.Context(), raw); err != nil {
				http.Error(w, "Failed to write.", http.StatusInternalServerError)
				return
			}
		case "delete":
			if err := entryDB.Delete(r.Context(), id); err != nil {
				http.Error(w, "Failed to delete.", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/admin", 302)
			return
		default:
			http.Error(w, "POST request failed to include action.", http.StatusBadRequest)
			return
		}
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
							      - POST action=update to update.
							      - POST action=delete to delete.
		    /admin/rollup
				            - A formatted post of the last N entries, used to create a rollup blog entry.

	*/

	r := mux.NewRouter()
	r.HandleFunc("/admin/new", adminNewHandler).Methods("POST")
	r.HandleFunc("/admin/edit/{id}", adminEditHandler).Methods("GET", "POST")
	r.HandleFunc("/admin", adminHandler).Methods("GET")
	r.HandleFunc("/feed", feedHandler).Methods("GET")
	r.HandleFunc("/", indexHandler).Methods("GET")

	http.Handle("/", r)
	log.Fatal(http.ListenAndServe(":"+config.PORT, nil))
}
