package component

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testComp implements Component for testing.
type testComp struct {
	id       string
	category string
	priority Priority
	initErr  error
	initDone bool
	closed   bool
}

func (tc *testComp) ID() string                      { return tc.id }
func (tc *testComp) Category() string                 { return tc.category }
func (tc *testComp) Priority() Priority               { return tc.priority }
func (tc *testComp) Init(_ *InitContext) error        { tc.initDone = true; return tc.initErr }
func (tc *testComp) Close() error                     { tc.closed = true; return nil }

var (
	_ Component             = (*testComp)(nil)
	_ CategorisedComponent  = (*testComp)(nil)
	_ PriorityProvider      = (*testComp)(nil)
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	comp := &testComp{id: "comp.1", category: "test"}
	reg.Register(comp)

	got := reg.Get("comp.1")
	assert.NotNil(t, got)
	assert.Equal(t, comp, got)
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := NewRegistry()
	got := reg.Get("nonexistent")
	assert.Nil(t, got)
}

func TestRegistry_ListByCategory_Empty(t *testing.T) {
	reg := NewRegistry()
	all := reg.ListByCategory("any")
	assert.Len(t, all, 0)
}

func TestRegistry_ListByCategory(t *testing.T) {
	reg := NewRegistry()
	c1 := &testComp{id: "cache.1", category: "cache"}
	c2 := &testComp{id: "cache.2", category: "cache"}
	c3 := &testComp{id: "reasoning.1", category: "reasoning"}

	reg.Register(c1)
	reg.Register(c2)
	reg.Register(c3)

	cacheComps := reg.ListByCategory("cache")
	assert.Len(t, cacheComps, 2)

	reasonComps := reg.ListByCategory("reasoning")
	assert.Len(t, reasonComps, 1)
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testComp{id: "dup"})

	// Registering same ID should overwrite, not panic.
	reg.Register(&testComp{id: "dup", category: "new-cat"})
	got := reg.Get("dup")
	assert.NotNil(t, got)
	// The newer one wins.
	assert.Equal(t, "new-cat", got.(*testComp).category)
}

func TestPriority_Constants(t *testing.T) {
	assert.True(t, PriorityObservability < PriorityEarly)
	assert.True(t, PriorityEarly < PriorityNormal)
	assert.True(t, PriorityNormal < PriorityLate)
	assert.True(t, PriorityLate < PriorityTerminal)
}

func TestPriority_Values(t *testing.T) {
	assert.Equal(t, Priority(-200), PriorityObservability)
	assert.Equal(t, Priority(-100), PriorityEarly)
	assert.Equal(t, Priority(0), PriorityNormal)
	assert.Equal(t, Priority(100), PriorityLate)
	assert.Equal(t, Priority(200), PriorityTerminal)
}

func TestInitContext_Fields(t *testing.T) {
	ctx := &InitContext{
		Lookup:    nil,
		LookupAll: nil,
		Pipeline:  nil,
	}
	assert.Nil(t, ctx.Lookup)
	assert.Nil(t, ctx.LookupAll)
	assert.Nil(t, ctx.Pipeline)
}

func TestComponent_Init(t *testing.T) {
	comp := &testComp{id: "init-test", category: "test"}
	assert.False(t, comp.initDone)

	ctx := &InitContext{}
	err := comp.Init(ctx)
	require.NoError(t, err)
	assert.True(t, comp.initDone)
}

func TestComponent_InitError(t *testing.T) {
	expectedErr := errors.New("init failed")
	comp := &testComp{id: "error-test", initErr: expectedErr}

	ctx := &InitContext{}
	err := comp.Init(ctx)
	assert.ErrorIs(t, err, expectedErr)
}

func TestComponent_Close(t *testing.T) {
	comp := &testComp{id: "close-test"}
	assert.False(t, comp.closed)

	assert.NoError(t, comp.Close())
	assert.True(t, comp.closed)
}

func TestRegistry_Orchestration(t *testing.T) {
	reg := NewRegistry()

	// Simulate what happens when an agent builds with multiple components.
	caches := []*testComp{
		{id: "cache.exact", category: "cache", priority: Priority(-100)},
		{id: "cache.semantic", category: "cache", priority: Priority(-50)},
	}
	reasoners := []*testComp{
		{id: "reasoning.cot", category: "reasoning", priority: PriorityNormal},
	}
	driver := &testComp{id: "driver.default", category: "driver", priority: Priority(200)}

	for _, c := range caches {
		reg.Register(c)
	}
	for _, r := range reasoners {
		reg.Register(r)
	}
	reg.Register(driver)

	// Verify we can discover by category.
	assert.Len(t, reg.ListByCategory("cache"), 2)
	assert.Len(t, reg.ListByCategory("reasoning"), 1)
	assert.Len(t, reg.ListByCategory("driver"), 1)
	assert.Len(t, reg.ListByCategory("unknown"), 0)

	// Verify priority ordering via individual lookup.
	got := reg.Get("cache.exact")
	assert.NotNil(t, got)
	assert.Equal(t, Priority(-100), got.(*testComp).priority)

	got = reg.Get("driver.default")
	assert.NotNil(t, got)
	assert.Equal(t, Priority(200), got.(*testComp).priority)
}

func TestRegistry_InitAll(t *testing.T) {
	reg := NewRegistry()
	comp := &testComp{id: "will-init", category: "test"}
	reg.Register(comp)

	assert.False(t, comp.initDone)

	// Simulate InitContext that would come from the pipeline.
	ctx := &InitContext{}

	// Init all components in the registry.
	all := reg.ListByCategory("test")
	for _, c := range all {
		require.NoError(t, c.Init(ctx))
	}

	assert.True(t, comp.initDone)
}

func TestComponent_WellKnownCategories(t *testing.T) {
	categories := []string{
		"reasoning", "compressor", "cache", "scheduler",
		"edge", "driver", "inference",
	}

	for _, cat := range categories {
		comp := &testComp{id: cat + ".test", category: cat}
		assert.Equal(t, cat, comp.Category())
	}
}

func TestRegistry_EmptyRegistry(t *testing.T) {
	reg := NewRegistry()
	assert.Len(t, reg.ListByCategory("anything"), 0)
}

func TestComponent_PartialInterface(t *testing.T) {
	// Verify that a Component does NOT need to implement CategorisedComponent.
	type basicComp struct {
		id string
	}

	bc := &basicComp{id: "basic"}

	// basicComp does not implement CategorisedComponent.
	_, ok := interface{}(bc).(CategorisedComponent)
	assert.False(t, ok)
}
