package history

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sviniabanditka/pinctl/internal/config"
	"github.com/sviniabanditka/pinctl/internal/engine"
	"github.com/sviniabanditka/pinctl/internal/model"
)

func TestRecord_RoundTripFirstObservationQueryable(t *testing.T) {
	s := openTestStore(t, testCfg())
	bus := newFakeBus()
	dev := testDevice("dev_1", model.RoleAnalogIn)
	devices := newFakeDevices(dev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Record(ctx, bus, devices)
		close(done)
	}()

	now := time.Now().UTC()
	bus.ch <- update(dev, engine.FloatValue(512), now)

	waitFor(t, 2*time.Second, func() bool { return countRows(t, s, "dev_1") == 1 })

	cancel()
	<-done

	samples, err := s.Query("dev_1", Query{From: now.Add(-time.Minute), To: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1", len(samples))
	}
	if samples[0].V != 512 {
		t.Errorf("V = %v, want 512", samples[0].V)
	}
}

func TestRecord_BoolStoredAsZeroOne(t *testing.T) {
	s := openTestStore(t, testCfg())
	bus := newFakeBus()
	dev := testDevice("dev_bool", model.RoleDigitalIn)
	devices := newFakeDevices(dev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Record(ctx, bus, devices)
		close(done)
	}()

	now := time.Now().UTC()
	bus.ch <- update(dev, engine.BoolValue(true), now)
	waitFor(t, 2*time.Second, func() bool { return countRows(t, s, "dev_bool") == 1 })
	cancel()
	<-done

	var v float64
	if err := s.db.QueryRow(`SELECT value FROM samples WHERE device_id = 'dev_bool'`).Scan(&v); err != nil {
		t.Fatalf("reading value: %v", err)
	}
	if v != 1.0 {
		t.Errorf("stored bool value = %v, want 1.0", v)
	}
}

func TestRecord_BatchesPerFlushIntervalNotPerSample(t *testing.T) {
	cfg := testCfg()
	cfg.FlushInterval = config.Duration(300 * time.Millisecond)
	s := openTestStore(t, cfg)
	bus := newFakeBus()
	dev := testDevice("dev_1", model.RoleAnalogIn)
	devices := newFakeDevices(dev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Record(ctx, bus, devices)
		close(done)
	}()
	defer func() { cancel(); <-done }()

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		bus.ch <- update(dev, engine.FloatValue(float64(500+i)), base.Add(time.Duration(i)*time.Millisecond))
	}

	// Give the samples time to reach Record's select loop, but well under
	// the flush interval: nothing should be in the database yet, because
	// a flush happens on the ticker (or at 512 buffered rows), not per
	// sample.
	time.Sleep(50 * time.Millisecond)
	if n := countRows(t, s, "dev_1"); n != 0 {
		t.Fatalf("rows before the flush interval elapsed = %d, want 0 (flush should be batched, not per-sample)", n)
	}

	waitFor(t, 2*time.Second, func() bool { return countRows(t, s, "dev_1") == 5 })
}

func TestRecord_DeletedDeviceUpdateSkipped(t *testing.T) {
	s := openTestStore(t, testCfg())
	bus := newFakeBus()
	dev := testDevice("dev_gone", model.RoleAnalogIn)
	devices := newFakeDevices(dev)
	devices.delete("dev_gone") // simulate: device already deleted before its Update arrives

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Record(ctx, bus, devices)
		close(done)
	}()

	bus.ch <- update(dev, engine.FloatValue(1), time.Now().UTC())

	// Flush a couple of times to be sure nothing shows up, then stop.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if n := countRows(t, s, "dev_gone"); n != 0 {
		t.Errorf("rows for a deleted device = %d, want 0", n)
	}
}

func TestRecord_QueryDuringFlushConcurrency(t *testing.T) {
	cfg := testCfg()
	cfg.FlushInterval = config.Duration(5 * time.Millisecond)
	s := openTestStore(t, cfg)
	bus := newFakeBus()
	dev := testDevice("dev_race", model.RoleAnalogIn)
	devices := newFakeDevices(dev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Record(ctx, bus, devices)
		close(done)
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		base := time.Now().UTC()
		for i := 0; i < 200; i++ {
			bus.ch <- update(dev, engine.FloatValue(float64(i)), base.Add(time.Duration(i)*time.Millisecond))
			time.Sleep(time.Millisecond)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := s.Query("dev_race", Query{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)}); err != nil {
				t.Errorf("concurrent Query: %v", err)
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
	wg.Wait()

	cancel()
	<-done
}
