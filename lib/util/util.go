package util

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"

	uuid "github.com/satori/go.uuid"
)

func NowISO8601() string {
	return time.Now().Format("2006-01-02T15:04:05.000000000Z07:00")
}

func GetUniqueIDFromUUID() string {
	return uuid.Must(uuid.NewV1(), nil).String()
}

func GenerateUUID() string {
	return uuid.Must(uuid.NewV4(), nil).String()
}

func GetUniqueIDFromDate() string {
	return NowISO8601()
}

type CheckerErrorStop struct{}

func (c CheckerErrorStop) Error() string {
	return "stop checker and return"
}

type CheckerFunc func(context.Context, interface{}, ...interface{}) (context.Context, error)

func Checker(ctx context.Context, checkFuncs ...CheckerFunc) func(interface{}, ...interface{}) (context.Context, error) {
	return func(target interface{}, args ...interface{}) (context.Context, error) {
		for _, f := range checkFuncs {
			var err error
			if ctx, err = f(ctx, target, args...); err != nil {
				return ctx, err
			}
		}

		return ctx, nil
	}
}

type SafeLock struct {
	lock  sync.Mutex
	locks int64
}

func (l *SafeLock) Lock() {
	if l.locks < 1 {
		l.lock.Lock()
	}
	atomic.AddInt64(&l.locks, 1)

	return
}

func (l *SafeLock) Unlock() {
	atomic.AddInt64(&l.locks, -1)
	if l.locks < 1 {
		l.lock.Unlock()
	}

	return
}

func GetENVValue(key, defaultValue string) (v string) {
	var found bool
	if v, found = os.LookupEnv(key); !found {
		return defaultValue
	}

	return
}

type SliceFlags []interface{}

func (s *SliceFlags) String() string {
	return "slice flags"
}

func (s *SliceFlags) Set(v string) error {
	if len(v) < 1 {
		return errors.New("empty string found")
	}

	*s = append(*s, v)
	return nil
}

func StripZero(b []byte) []byte {
	var n int
	if n = bytes.Index(b, []byte("\x00")); n != -1 {
		b = b[:n]
	}
	if n = bytes.LastIndex(b, []byte("\x00")); n != -1 {
		b = b[n+1:]
	}

	return b
}

func GetUrlQuery(query url.Values, key, defaultValue string) string {
	v := query.Get(key)
	if len(v) > 0 {
		return v
	}

	return defaultValue
}

func InTestVerbose() bool {
	flag.Parse()
	if v := flag.Lookup("test.v"); v == nil || v.Value.String() != "true" {
		return false
	}

	return true
}

func InTest() bool {
	flag.Parse()
	if v := flag.Lookup("test.v"); v == nil {
		return false
	}

	return true
}
