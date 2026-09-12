package authorization_test

import (
	"context"
	"testing"

	"github.com/openfga/openfga/pkg/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/vishu42/tflive/internal/authorization"
)

func TestNewRequiresAPool(t *testing.T) {
	_, err := authorization.New(context.Background(), nil, "tflive")
	require.Error(t, err, "a server with no datastore must refuse to exist")
}

func TestBootstrapResolvesAStoreAndModel(t *testing.T) {
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	require.NoError(t, err)
	t.Cleanup(auth.Close)

	require.NotEmpty(t, auth.StoreID())
	require.NotEmpty(t, auth.ModelID())
}

func TestCloseIsSafeTwice(t *testing.T) {
	// New closes on a failed bootstrap and callers also defer it, so a double
	// free would turn a startup error into a panic.
	auth, err := authorization.NewWithDatastore(context.Background(), memory.New(), "tflive-test")
	require.NoError(t, err)
	auth.Close()
	auth.Close()
}

// TestCloseLeavesTheBorrowedPoolUsable guards a bug that would otherwise only
// surface at shutdown: OpenFGA's own Datastore.Close calls primaryDB.Close(),
// which would shut down the application's shared pool -- the one serving every
// repository, the queue and the session store.
func TestCloseLeavesTheBorrowedPoolUsable(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	auth, err := authorization.New(ctx, pool, "tflive-close-test")
	require.NoError(t, err)
	auth.Close()

	require.NoError(t, pool.Ping(ctx), "closing authorization must not close the pool it borrowed")
}

// TestBootstrapIsIdempotentAcrossRestarts is the one that matters: a restart
// must adopt the same store and the same model. If it does not, every boot
// mints a new model id and every existing tuple is evaluated against a model
// nothing was ever written against.
func TestBootstrapIsIdempotentAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)

	first, err := authorization.New(ctx, pool, "tflive-restart-test")
	require.NoError(t, err)
	firstStore, firstModel := first.StoreID(), first.ModelID()
	first.Close()

	second, err := authorization.New(ctx, pool, "tflive-restart-test")
	require.NoError(t, err)
	t.Cleanup(second.Close)

	require.Equal(t, firstStore, second.StoreID(), "a restart must adopt the same store")
	require.Equal(t, firstModel, second.ModelID(), "a restart must adopt the same model")
}
