package srog

import (
	"io"
	"sync"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// Interval selects time-based rotation cadence for a file sink. It composes
// with size-based rotation: a file rolls over when either trigger fires.
type Interval uint8

const (
	// NoInterval disables time-based rotation (size-based only, if configured).
	NoInterval Interval = iota
	// Hourly rotates at the top of every hour.
	Hourly
	// Daily rotates at midnight.
	Daily
)

// Rotation configures rotation and retention for a file sink. The zero value
// performs no rotation. Size-based rotation triggers when the active file
// exceeds MaxSizeMB; time-based rotation triggers when Every elapses.
type Rotation struct {
	// MaxSizeMB rotates the file once it grows beyond this many megabytes.
	// Zero means no size-based trigger.
	MaxSizeMB int
	// MaxBackups caps how many rotated files are retained (0 = keep all).
	MaxBackups int
	// MaxAgeDays deletes rotated files older than this many days (0 = no limit).
	MaxAgeDays int
	// Compress gzips rotated files.
	Compress bool
	// LocalTime uses local time (instead of UTC) in backup timestamps and for
	// computing rotation boundaries.
	LocalTime bool
	// Every selects an additional time-based rotation cadence.
	Every Interval
}

// enabled reports whether any rotation behavior is configured.
func (r Rotation) enabled() bool {
	return r.MaxSizeMB > 0 || r.MaxBackups > 0 || r.MaxAgeDays > 0 || r.Compress || r.Every != NoInterval
}

// sizeNoCapMB is the effective "unlimited" size cap when only time-based
// rotation is requested; lumberjack treats 0 as its 100MB default, which we do
// not want to impose implicitly.
const sizeNoCapMB = 1 << 20 // 1 PB — effectively unbounded

// newRotatingWriter builds the file writer for a sink, layering time-based
// rotation over lumberjack's size/age/backup management as configured.
func newRotatingWriter(path string, r Rotation) (io.WriteCloser, error) {
	maxSize := r.MaxSizeMB
	if maxSize <= 0 {
		maxSize = sizeNoCapMB
	}
	lj := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    maxSize,
		MaxBackups: r.MaxBackups,
		MaxAge:     r.MaxAgeDays,
		Compress:   r.Compress,
		LocalTime:  r.LocalTime,
	}
	if r.Every == NoInterval {
		return lj, nil
	}
	return &intervalWriter{lj: lj, every: r.Every, localTime: r.LocalTime}, nil
}

// intervalWriter wraps a lumberjack logger and forces a rotation when the
// configured time boundary is crossed. lumberjack's own mutex guards the write;
// a separate mutex guards the period bookkeeping.
type intervalWriter struct {
	lj        *lumberjack.Logger
	every     Interval
	localTime bool

	mu      sync.Mutex
	current int64 // bucket index of the period the active file belongs to
}

func (w *intervalWriter) Write(p []byte) (int, error) {
	bucket := periodBucket(timeNow(), w.every, w.localTime)
	w.mu.Lock()
	if w.current == 0 {
		w.current = bucket
	} else if bucket != w.current {
		// Boundary crossed: roll the file before writing the new period's line.
		_ = w.lj.Rotate()
		w.current = bucket
	}
	w.mu.Unlock()
	return w.lj.Write(p)
}

func (w *intervalWriter) Close() error { return w.lj.Close() }

// periodBucket maps t to a monotonically increasing bucket index for the given
// cadence, so a change in bucket signals a boundary crossing.
func periodBucket(t time.Time, every Interval, local bool) int64 {
	if !local {
		t = t.UTC()
	}
	switch every {
	case Hourly:
		return t.Unix() / 3600
	case Daily:
		return t.Unix() / 86400
	default:
		return 0
	}
}

// timeNow is indirected for testability.
var timeNow = time.Now
