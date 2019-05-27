package entries

import (
	"context"
	"crypto/md5"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"time"

	"google.golang.org/api/iterator"

	"github.com/jcgregorio/go-lib/ds"
	"github.com/jcgregorio/slog"
)

const (
	ENTRY ds.Kind = "Entry"
)

type Entries struct {
	DS  *ds.DS
	log slog.Logger
}

func New(ctx context.Context, project, ns string, log slog.Logger) (*Entries, error) {
	d, err := ds.New(ctx, project, ns)
	if err != nil {
		return nil, err
	}
	return &Entries{
		DS:  d,
		log: log,
	}, nil
}

type Entry struct {
	Title   string    `datastore:"title,noindex"`
	Content string    `datastore:"content,noindex"`
	ID      string    `datastore:"-"`
	Created time.Time `datastore:"created"`
}

func (e *Entries) Get(ctx context.Context, id string) (*Entry, error) {
	key := e.DS.NewKey(ENTRY)
	key.Name = id

	var entry Entry
	if err := e.DS.Client.Get(ctx, key, &entry); err != nil {
		return nil, fmt.Errorf("Failed to load %s: %s", key, err)
	} else {
		entry.ID = id
		return &entry, nil
	}
}

func (e *Entries) Insert(ctx context.Context, content, title string) (string, error) {
	key := e.DS.NewKey(ENTRY)
	key.Name = fmt.Sprintf("%x", md5.Sum([]byte(content+title+time.Now().Format(time.RFC3339Nano))))

	entry := &Entry{
		Content: content,
		Title:   title,
		Created: time.Now(),
	}
	_, err := e.DS.Client.Put(context.Background(), key, entry)
	return key.Name, err
}

func (e *Entries) Update(ctx context.Context, id, content, title string) error {
	key := e.DS.NewKey(ENTRY)
	key.Name = id

	entry := &Entry{
		Content: content,
		Title:   title,
		Created: time.Now(),
	}
	_, err := e.DS.Client.Put(context.Background(), key, entry)
	return err
}

func (e *Entries) Delete(ctx context.Context, id string) error {
	key := e.DS.NewKey(ENTRY)
	key.Name = id
	return e.DS.Client.Delete(context.Background(), key)
}

func (e *Entries) List(ctx context.Context, n int, offset int) ([]*Entry, error) {
	ret := []*Entry{}
	q := e.DS.NewQuery(ENTRY).Order("-created").Limit(n).Offset(offset)

	it := e.DS.Client.Run(ctx, q)
	for {
		entry := &Entry{}
		key, err := it.Next(entry)
		if err == iterator.Done {
			break
		}
		if err != nil {
			e.log.Infof("Failed while reading: %s", err)
			break
		}
		entry.ID = key.Name
		ret = append(ret, entry)
	}
	return ret, nil
}
