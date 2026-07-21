package clock

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRealClock_Now(t *testing.T) {
	c := RealClock{}
	now := c.Now()
	assert.False(t, now.IsZero())
	assert.WithinDuration(t, time.Now(), now, time.Second)
}

func TestRealClock_After(t *testing.T) {
	c := RealClock{}
	start := c.Now()
	<-c.After(50 * time.Millisecond)
	assert.WithinDuration(t, start.Add(50*time.Millisecond), c.Now(), 20*time.Millisecond)
}

func TestRealClock_NewTicker(t *testing.T) {
	c := RealClock{}
	tk := c.NewTicker(10 * time.Millisecond)
	defer tk.Stop()

	select {
	case <-tk.C():
		// fired
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ticker did not fire")
	}
}

func TestRealClock_TickerStop(t *testing.T) {
	c := RealClock{}
	tk := c.NewTicker(5 * time.Millisecond)
	<-tk.C() // consume first tick
	tk.Stop()

	select {
	case <-tk.C():
		t.Fatal("ticker fired after stop")
	case <-time.After(50 * time.Millisecond):
		// ok, stopped
	}
}

func TestFakeClock_NewFakeClock(t *testing.T) {
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)
	assert.Equal(t, now, fc.Now())
}

func TestFakeClock_Advance(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)

	fc.Advance(30 * time.Minute)
	assert.Equal(t, now.Add(30*time.Minute), fc.Now())

	fc.Advance(1 * time.Hour)
	assert.Equal(t, now.Add(90*time.Minute), fc.Now())
}

func TestFakeClock_Ticker(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)

	tk := fc.NewTicker(100 * time.Millisecond)

	// Before advance, ticker should not have fired.
	select {
	case <-tk.C():
		t.Fatal("ticker fired before advance")
	default:
	}

	fc.Advance(100 * time.Millisecond)
	select {
	case <-tk.C():
		// ok
	default:
		t.Fatal("ticker did not fire after advancing by period")
	}

	// Second advance should fire again.
	fc.Advance(100 * time.Millisecond)
	select {
	case <-tk.C():
		// ok
	default:
		t.Fatal("ticker did not fire on second advance")
	}

	tk.Stop()
}

func TestFakeClock_After(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)

	ch := fc.After(500 * time.Millisecond)

	select {
	case <-ch:
		t.Fatal("After fired before advance")
	default:
	}

	fc.Advance(500 * time.Millisecond)
	select {
	case <-ch:
		// ok
	default:
		t.Fatal("After did not fire after advance")
	}
}

func TestFakeClock_After_FiresOnce(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)

	ch := fc.After(100 * time.Millisecond)
	fc.Advance(200 * time.Millisecond)

	<-ch // consume

	// After should NOT fire again even if we advance more.
	fc.Advance(200 * time.Millisecond)
	select {
	case <-ch:
		t.Fatal("After fired twice")
	default:
	}
}

func TestClock_Interface(t *testing.T) {
	var c Clock
	c = RealClock{}
	assert.NotNil(t, c)

	fc := NewFakeClock(time.Now())
	c = fc
	assert.NotNil(t, c)
}

func TestTicker_Interface(t *testing.T) {
	var tk Ticker
	tk = &realTicker{ticker: time.NewTicker(time.Hour)}
	defer tk.Stop()
	assert.NotNil(t, tk)
}

func TestFakeClock_MultipleTickers(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := NewFakeClock(now)

	tk1 := fc.NewTicker(50 * time.Millisecond)
	tk2 := fc.NewTicker(100 * time.Millisecond)

	fc.Advance(60 * time.Millisecond)
	select {
	case <-tk1.C():
		// 50ms ticker fired
	default:
		t.Fatal("50ms ticker did not fire")
	}
	select {
	case <-tk2.C():
		t.Fatal("100ms ticker fired too early")
	default:
	}

	tk1.Stop()
	tk2.Stop()
}
