package entries

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jcgregorio/logger"
	"github.com/stretchr/testify/assert"
)

// InitDatastore is a common utility function used in tests. It sets up the
// datastore to connect to the emulator and also clears out all instances of
// the given 'kinds' from the datastore.
func InitForTesting(t assert.TestingT) *Entries {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	emulatorHost := os.Getenv("DATASTORE_EMULATOR_HOST")
	if emulatorHost == "" {
		assert.Fail(t, `Running tests that require a running Cloud Datastore emulator.

Run

	"gcloud beta emulators datastore start --no-store-on-disk --host-port=localhost:8888"

and then run

  $(gcloud beta emulators datastore env-init)

to set the environment variables. When done running tests you can unset the env variables:

  $(gcloud beta emulators datastore env-unset)

`)
	}

	// Do a quick healthcheck against the host, which will fail immediately if it's down.
	_, err := http.DefaultClient.Get("http://" + emulatorHost + "/")
	assert.NoError(t, err, fmt.Sprintf("Cloud emulator host %s appears to be down or not accessible.", emulatorHost))

	e, err := New(context.Background(), "test-project", fmt.Sprintf("test-namespace-%d", r.Uint64()), logger.New())
	assert.NoError(t, err)
	return e
}

func TestDB(t *testing.T) {
	e := InitForTesting(t)
	ctx := context.Background()
	entries, err := e.List(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, entries, 0)

	id, err := e.Insert(ctx, "This is content.", "This is title")
	assert.NoError(t, err)
	assert.NotEqual(t, id, "")

	entries, err = e.List(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, entries[0].ID, id)
	assert.Equal(t, entries[0].Title, "This is title")
	assert.Equal(t, entries[0].Content, "This is content.")

	id2, err := e.Insert(ctx, "This is content.", "This is another post")
	assert.NoError(t, err)
	assert.NotEqual(t, id2, "")
	assert.NotEqual(t, id2, id)

	entries, err = e.List(ctx, 1, 0)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)

	entries, err = e.List(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, entries[0].ID, id2)
	assert.Equal(t, entries[0].Title, "This is another post")
	assert.Equal(t, entries[0].Content, "This is content.")
	assert.Equal(t, entries[1].ID, id)
	assert.Equal(t, entries[1].Title, "This is title")
	assert.Equal(t, entries[1].Content, "This is content.")

	entries, err = e.List(ctx, 10, 1)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, entries[0].ID, id)
	assert.Equal(t, entries[0].Title, "This is title")
	assert.Equal(t, entries[0].Content, "This is content.")

	err = e.Delete(ctx, id)
	assert.NoError(t, err)

	entries, err = e.List(ctx, 10, 0)
	assert.NoError(t, err)
	assert.Len(t, entries, 1)
	assert.Equal(t, entries[0].ID, id2)
	assert.Equal(t, entries[0].Title, "This is another post")
	assert.Equal(t, entries[0].Content, "This is content.")
}
