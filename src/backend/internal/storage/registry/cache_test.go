package registry

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/chan/agentify/backend/internal/models"
)

// fakeStore is an in-memory PodStore that can be told to fail ListPods.
type fakeStore struct {
	pods      map[string]*models.Pod
	listErr   error
	listCalls int
}

func newFake(pods ...*models.Pod) *fakeStore {
	m := map[string]*models.Pod{}
	for _, p := range pods {
		m[p.ID] = p
	}
	return &fakeStore{pods: m}
}

func (f *fakeStore) ListPods(_ context.Context, filter *models.PodFilter) ([]*models.Pod, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []*models.Pod
	for _, p := range f.pods {
		out = append(out, p)
	}
	if filter != nil {
		out = filterPods(out, filter)
	}
	return out, nil
}
func (f *fakeStore) UpsertPod(_ context.Context, p *models.Pod) error { f.pods[p.ID] = p; return nil }
func (f *fakeStore) DeletePod(_ context.Context, id string) error    { delete(f.pods, id); return nil }
func (f *fakeStore) GetPod(_ context.Context, id string) (*models.Pod, error) {
	if p, ok := f.pods[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}
func (f *fakeStore) ListPodsByNamespace(c context.Context, ns string) ([]*models.Pod, error) {
	return f.ListPods(c, &models.PodFilter{Namespace: &ns})
}
func (f *fakeStore) ListActivePods(c context.Context) ([]*models.Pod, error) { return f.ListPods(c, nil) }
func (f *fakeStore) UpdateQueryStats(context.Context, string, bool, int64) error { return nil }
func (f *fakeStore) UpdateFreshness(context.Context, string, int64) error        { return nil }
func (f *fakeStore) GetStats(context.Context) (*models.PodStats, error)          { return nil, nil }

func pod(id string) *models.Pod { return &models.Pod{ID: id, Lifecycle: "active"} }

func TestCacheHitAndInvalidate(t *testing.T) {
	f := newFake(pod("a"))
	c := NewCache(f, time.Minute, time.Minute, slog.Default())
	ctx := context.Background()

	if _, err := c.GetPod(ctx, "a"); err != nil || f.listCalls != 1 {
		t.Fatalf("first read: err=%v listCalls=%d (want 1)", err, f.listCalls)
	}
	// Within TTL → served from cache, no new ListPods.
	if _, err := c.GetPod(ctx, "a"); err != nil || f.listCalls != 1 {
		t.Fatalf("cached read: err=%v listCalls=%d (want still 1)", err, f.listCalls)
	}
	// A new pod via UpsertPod must invalidate → next read refreshes and sees it.
	if err := c.UpsertPod(ctx, pod("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetPod(ctx, "b"); err != nil || f.listCalls != 2 {
		t.Fatalf("post-upsert read: err=%v listCalls=%d (want 2)", err, f.listCalls)
	}
}

func TestCacheServesStaleOnError(t *testing.T) {
	f := newFake(pod("a"))
	c := NewCache(f, time.Millisecond, time.Minute, slog.Default())
	ctx := context.Background()

	if _, err := c.GetPod(ctx, "a"); err != nil { // warm the snapshot
		t.Fatal(err)
	}
	f.listErr = errors.New("dynamodb down")
	time.Sleep(5 * time.Millisecond) // past TTL → forces a refresh attempt

	got, err := c.GetPod(ctx, "a")
	if err != nil || got == nil || got.ID != "a" {
		t.Fatalf("expected stale-served pod, got err=%v pod=%v", err, got)
	}
}

func TestCacheErrorsPastMaxStale(t *testing.T) {
	f := newFake(pod("a"))
	c := NewCache(f, time.Millisecond, 0, slog.Default()) // maxStale=0 → never serve stale
	ctx := context.Background()

	if _, err := c.GetPod(ctx, "a"); err != nil { // warm
		t.Fatal(err)
	}
	f.listErr = errors.New("dynamodb down")
	time.Sleep(5 * time.Millisecond) // past TTL and past maxStale

	if _, err := c.GetPod(ctx, "a"); err == nil {
		t.Fatal("expected error past max-stale, got nil")
	}
}
