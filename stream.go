package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	units "github.com/docker/go-units"
	"github.com/gorilla/mux"
	"github.com/spf13/viper"
	blackfriday "gopkg.in/russross/blackfriday.v2"

	"github.com/jcgregorio/go-lib/admin"
	"github.com/jcgregorio/logger"
	"github.com/jcgregorio/stream-run/entries"
	"willnorris.com/go/webmention"
)

// Config keys as found in config.json.
const (
	DATASTORE_NAMESPACE = "DATASTORE_NAMESPACE"
	CLIENT_ID           = "CLIENT_ID"
	REGION              = "REGION"
	PROJECT             = "PROJECT"
	ADMINS              = "ADMINS"
	HOST                = "HOST"
	AUTHOR              = "AUTHOR"
	WEBSUB              = "WEBSUB"
	BRIDGES             = "BRIDGES"
	FEDSOC_BRIDGE       = "FEDSOC_BRIDGE"
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

	ad *admin.Admin
)

func permalinkFromId(id string) string {
	return fmt.Sprintf("%s/entry/%s", viper.GetString(HOST), id)
}

func loadTemplates() {
	pattern := filepath.Join(*resourcesDir, "templates", "*.*")

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
			return units.HumanDuration(time.Now().Sub(t)) + " ago"
		},
		"atomTime": func(t time.Time) string {
			return t.Format(time.RFC3339)
		},
	})
	template.Must(templates.ParseGlob(pattern))
}

func initialize() {
	flag.Parse()
	viper.SetConfigType("json")
	if *resourcesDir == "" {
		_, filename, _, _ := runtime.Caller(0)
		*resourcesDir = filepath.Join(filepath.Dir(filename))
	}

	f, err := os.Open(filepath.Join(*resourcesDir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := viper.ReadConfig(f); err != nil {
		log.Fatal(err)
	}

	viper.AddConfigPath(*resourcesDir)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatal(err)
	}

	ad = admin.New(viper.GetString(CLIENT_ID), viper.GetStringSlice(ADMINS))
	loadTemplates()

	entryDB, err = entries.New(context.Background(), viper.GetString(PROJECT), viper.GetString(DATASTORE_NAMESPACE), log)
	if err != nil {
		log.Fatal(err)
	} else {
		log.Info("Initialized.")
	}
}

type adminContext struct {
	IsAdmin bool
	Entries []*entryContent
	Offset  int
	Config  map[string]interface{}
	Form    map[string]string
}

type entryContent struct {
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

// The returned map has values for 'title' and 'content'.
//
// For example Chrome on Android shares the title: and from Twitter web that looks like:
//   <user name> on Twitter: "full tweet text <t.co link>" / Twitter
// and text: is the url of the tweet.
func shareTargetToMap(form url.Values) map[string]string {
	ret := map[string]string{}
	ret["title"] = form.Get("title")
	ret["content"] = form.Get("text")

	// Presume that all links are coming from Chrome, so text: is the url most of
	// the time, but not always, you can select text to share, and that shows up
	// w/o a URL.
	u := form.Get("text")
	if _, err := url.Parse(u); err != nil {
		u = ""
	}
	if u == "" {
		u = form.Get("url")
	}
	if u != "" {
		doc, err := goquery.NewDocument(u)
		if err != nil {
			log.Infof("goquery failed to parse %q: %s", u, err)
			return ret
		}
		u = doc.Find("link[rel=canonical]").AttrOr("href", u)
		ret["title"] = doc.Find("title").Contents().Text()
		ret["content"] = fmt.Sprintf("<a class='u-in-reply-to' href='%s'>%s</a>", u, ret["title"])
	}
	return ret
}

// adminHandler displays the admin page for Stream.
//
// They query parameters 'title', 'text', and 'url' may be supplied by a Web
// Share Target call and should pre-populate the form for creating a new
// entry.
func adminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	context := &adminContext{}
	isAdmin := ad.IsAdmin(r, log)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form values.", 400)
		return
	}
	context = &adminContext{
		IsAdmin: isAdmin,
		Config:  viper.AllSettings(),
		Form:    shareTargetToMap(r.Form),
	}
	log.Infof("Form: %#v", context.Form)
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
		if len(entries) < limit {
			context.Offset = -1
		}
	}
	if err := templates.ExecuteTemplate(w, "admin.html", context); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
}

type indexContext struct {
	Config  map[string]interface{}
	Entries []*entryContent
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
	log.Infof("%#v\n", viper.AllSettings())
	context := &indexContext{
		Config:  viper.AllSettings(),
		Entries: toDisplaySlice(entries),
		Offset:  int(offset + limit),
	}
	if len(entries) < limit {
		context.Offset = -1
	}
	if err := templates.ExecuteTemplate(w, "index.html", context); err != nil {
		log.Errorf("Failed to render index template: %s", err)
	}
}

type feedContext struct {
	Updated time.Time
	Entries []*entryContent
	Config  map[string]interface{}
	Author  string
	Host    string
}

// feedHandler displays the admin page for Stream.
func feedHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/atom+xml")
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
		Config:  viper.AllSettings(),
		Updated: updated,
		Entries: toDisplaySlice(entries),
	}
	if err := templates.ExecuteTemplate(w, "atom.xml", context); err != nil {
		log.Errorf("Failed to render index template: %s", err)
	}
}

func toDisplayContent(s string) string {
	content := strings.ReplaceAll(s, "\r\n", "\n")
	bridges := []string{}
	for _, href := range viper.GetStringSlice(BRIDGES) {
		bridges = append(bridges, fmt.Sprintf("<a href='%s'></a>", href))
	}

	return string(blackfriday.Run([]byte(content))) + strings.Join(bridges, " ")
}

// toDisplay converts an entries.Entry into an entryContent.
func toDisplay(in *entries.Entry) *entryContent {
	content := toDisplayContent(in.Content)
	return &entryContent{
		Title:       in.Title,
		Content:     template.HTML(content),
		SafeContent: content,
		ID:          in.ID,
		Created:     in.Created,
		Updated:     in.Updated,
	}
}

func toDisplaySlice(in []*entries.Entry) []*entryContent {
	ret := []*entryContent{}
	for _, en := range in {
		ret = append(ret, toDisplay(en))
	}
	return ret
}

// adminNewHandler accepts POST'd form values to create a new entry.
func adminNewHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	if !ad.IsAdmin(r, log) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	content := r.FormValue("content")
	id, err := entryDB.Insert(r.Context(), content, r.FormValue("title"))
	if err != nil {
		log.Errorf("Failed to insert: %s", err)
		http.Error(w, "Failed to insert", http.StatusInternalServerError)
	}
	if err := sendWebMentions(id, toDisplayContent(content)); err != nil {
		log.Warningf("Failed to send webmentions: %s", err)
	}
	http.Redirect(w, r, "/admin", 302)
}

func sendWebMentions(id, content string) error {
	client := &http.Client{
		Timeout: time.Second * 30,
	}
	source := permalinkFromId(id)
	m := webmention.New(client)
	buf := bytes.NewBufferString(content)
	links, err := webmention.DiscoverLinksFromReader(buf, source, "")
	if err != nil {
		return fmt.Errorf("Failed to discover links in %q: %s", content, err)
	}
	for _, link := range links {
		log.Infof("Webmention trying to send: %q -> %q", source, link)
		endpoint, err := m.DiscoverEndpoint(link)
		if err != nil {
			return err
		}
		resp, err := m.SendWebmention(endpoint, source, link)
		if err != nil {
			log.Infof("Failed to send webmention %q -> %q: %s", source, link, err)
		} else if resp.StatusCode >= 400 {
			log.Infof("Failed to send webmention %q -> %q: Status code %d:%s: %s", source, link, resp.StatusCode, resp.Status, err)
		} else {
			log.Infof("Webmention sent: %q -> %q", source, link)
		}
	}
	websubUrl := viper.GetString(WEBSUB)
	resp, err := client.PostForm(websubUrl, url.Values{
		"hub.mode": {"publish"},
		"hub.url":  {fmt.Sprintf("%s/feed", viper.GetString(HOST))},
	})
	if err != nil {
		log.Errorf("Failed to update websub hub: %q: %s", websubUrl, err)
	}
	log.Infof("WebSub response: %d - %q", resp.StatusCode, resp.Status)

	return nil
}

type editContext struct {
	Raw    *entries.Entry
	Cooked *entryContent
	Config map[string]interface{}
}

// adminEditHandler displays the admin page for Stream.
func adminEditHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	if !ad.IsAdmin(r, log) {
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
			cooked := toDisplay(raw)
			if err := sendWebMentions(id, cooked.SafeContent); err != nil {
				log.Warningf("Failed to send webmentions: %s", err)
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
	c := editContext{
		Raw:    raw,
		Cooked: toDisplay(raw),
		Config: viper.AllSettings(),
	}
	if err := templates.ExecuteTemplate(w, "adminEdit.html", c); err != nil {
		log.Errorf("Failed to render admin template: %s", err)
	}
}

type entryContext struct {
	Cooked *entryContent
	Config map[string]interface{}
}

// entryHandler handles the permalink for an individual entry.
func entryHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	vars := mux.Vars(r)
	id := vars["id"]
	raw, err := entryDB.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	c := &entryContext{
		Cooked: toDisplay(raw),
		Config: viper.AllSettings(),
	}

	if err := templates.ExecuteTemplate(w, "entry.html", c); err != nil {
		log.Errorf("Failed to render entry template: %s", err)
	}
}

// serviceWorkerHandler handles the permalink for an individual entry.
func serviceWorkerHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	w.Header().Set("Content-Type", "text/javascript")
	if err := templates.ExecuteTemplate(w, "service-worker.js", nil); err != nil {
		log.Errorf("Failed to render service-worker.js: %s", err)
	}
}

// manifestHandler handles the permalink for an individual entry.
func manifestHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	w.Header().Set("Content-Type", "application/json")
	if err := templates.ExecuteTemplate(w, "manifest.json", nil); err != nil {
		log.Errorf("Failed to render manifest.json: %s", err)
	}
}

// offlineHandler handles the permalink for an individual entry.
func offlineHandler(w http.ResponseWriter, r *http.Request) {
	if *local {
		loadTemplates()
	}
	w.Header().Set("Content-Type", "text/html")
	if err := templates.ExecuteTemplate(w, "offline.html", nil); err != nil {
		log.Errorf("Failed to render service-worker.js: %s", err)
	}
}

func makeImagesHandler() func(http.ResponseWriter, *http.Request) {
	fileServer := http.FileServer(http.Dir(filepath.Join(*resourcesDir, "images")))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "max-age=300")
		fileServer.ServeHTTP(w, r)
	}
}

func makeRedirectHandler(path string) func(http.ResponseWriter, *http.Request) {
	domain := viper.GetString(FEDSOC_BRIDGE)
	return func(w http.ResponseWriter, r *http.Request) {
		u := domain + path + r.URL.RawQuery
		log.Infof("Redirecting to: %q", u)
		http.Redirect(w, r, u, 302)
	}
}

func main() {
	initialize()
	/*

			/            - Root, displays the last 10 stream entries. Link to feed.
				             Link to admin page. Link to rollup page. Links to entry permalinks.
			/entry/<id>  - Permalink for each entry.
			/feed        - Atom feed of last 10 stream entries.
			/admin       - Must be logged in and admin to access. Allows creating/editing/deleting stream entries.
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
	r.PathPrefix("/images/").Handler(http.StripPrefix("/images/", http.HandlerFunc(makeImagesHandler()))).Methods("GET", "HEAD")
	r.HandleFunc("/admin/new", adminNewHandler).Methods("POST")
	r.HandleFunc("/admin/edit/{id}", adminEditHandler).Methods("GET", "POST")
	r.HandleFunc("/admin", adminHandler).Methods("GET")
	r.HandleFunc("/feed", feedHandler).Methods("GET", "HEAD")
	r.HandleFunc("/", indexHandler).Methods("GET", "HEAD")
	r.HandleFunc("/entry/{id}", entryHandler).Methods("GET", "HEAD")
	r.HandleFunc("/service-worker.js", serviceWorkerHandler).Methods("GET")
	r.HandleFunc("/offline", offlineHandler).Methods("GET")
	r.HandleFunc("/manifest.json", manifestHandler).Methods("GET", "HEAD")
	r.HandleFunc("/.well-known/host-meta", makeRedirectHandler("/.well-known/host-meta")).Methods("GET", "HEAD")
	r.HandleFunc("/.well-known/host-meta.xrd", makeRedirectHandler("/.well-known/host-meta.xrd")).Methods("GET", "HEAD")
	r.HandleFunc("/.well-known/host-meta.jrd", makeRedirectHandler("/.well-known/host-meta.jrd")).Methods("GET", "HEAD")
	r.HandleFunc("/.well-known/webfinger", makeRedirectHandler("/.well-known/webfinger")).Methods("GET", "HEAD")

	http.Handle("/", r)
	port := os.Getenv("PORT")
	if port == "" {
		port = "1313"
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
