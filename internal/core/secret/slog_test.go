package secret_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/gsoares85/hermes/internal/core/secret"
)

// The connection string these tests log, and the one substring none of them is
// allowed to find in the output.
const (
	dsn      = "postgres://hermes:s3cr3t@db.example.com:5432/app?sslmode=require"
	password = "s3cr3t"
)

// A value that renders itself only when something asks it to, which is the
// shape a lazily built connection string arrives in.
type lazyDSN struct{ text string }

func (l lazyDSN) LogValue() slog.Value { return slog.StringValue(l.text) }

// logged runs the function against a logger whose handler is the redacting one,
// and returns what the handler at the end of the chain wrote.
func logged(t *testing.T, emit func(*slog.Logger)) string {
	t.Helper()

	var out bytes.Buffer
	emit(slog.New(secret.NewHandler(slog.NewJSONHandler(&out, nil))))

	return out.String()
}

// The test the plan names: a full DSN goes into the logger and the password
// does not come out the other side.
func TestHandlerRedactsTheMessage(t *testing.T) {
	t.Parallel()

	line := logged(t, func(logger *slog.Logger) {
		logger.Info("connecting to " + dsn + " failed")
	})

	if strings.Contains(line, password) {
		t.Errorf("the handler wrote %q, the secret survived", line)
	}
	for _, kept := range []string{"db.example.com", "failed"} {
		if !strings.Contains(line, kept) {
			t.Errorf("the handler wrote %q, want it to keep %q", line, kept)
		}
	}
}

// A secret reaches a log as an attribute at least as often as it reaches one in
// the message, and every shape an attribute has is a shape it can hide in.
func TestHandlerRedactsAttributes(t *testing.T) {
	t.Parallel()

	cases := map[string]func(*slog.Logger){
		"a string": func(logger *slog.Logger) {
			logger.Info("opening", slog.String("dsn", dsn))
		},
		"an error": func(logger *slog.Logger) {
			logger.Error("opening", slog.Any("error", errors.New("dialing "+dsn)))
		},
		"a group": func(logger *slog.Logger) {
			logger.Info("opening", slog.Group("connection", slog.String("dsn", dsn)))
		},
		"a value resolved on demand": func(logger *slog.Logger) {
			logger.Info("opening", slog.Any("dsn", lazyDSN{text: dsn}))
		},
		"a value that is neither": func(logger *slog.Logger) {
			logger.Info("opening", slog.Any("tried", []string{dsn}))
		},
		// The shape the handler itself produces. redactAny renders an
		// unrecognised value with %+v, so a struct carrying a password used to
		// go in as a value and come out as text with the password in it — the
		// net leaking through the one format it generates.
		"a struct logged whole": func(logger *slog.Logger) {
			logger.Info("saving", slog.Any("form", struct {
				Host     string
				Password string
			}{Host: "db.example.com", Password: password}))
		},
		"a map logged whole": func(logger *slog.Logger) {
			logger.Info("saving", slog.Any("form", map[string]string{
				"host": "db.example.com", "password": password,
			}))
		},
		"a keyword connection string": func(logger *slog.Logger) {
			logger.Info("opening", slog.String("dsn", "host=localhost password="+password))
		},
		"the form the window sends": func(logger *slog.Logger) {
			logger.Info(`binding call args {"host":"localhost","password":"` + password + `"}`)
		},
	}

	for name, emit := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if line := logged(t, emit); strings.Contains(line, password) {
				t.Errorf("the handler wrote %q, the secret survived", line)
			}
		})
	}
}

// Attributes bound to a logger are written on every record it emits, so a
// secret that gets past this one gets past it repeatedly.
func TestHandlerRedactsAttributesBoundToTheLogger(t *testing.T) {
	t.Parallel()

	line := logged(t, func(logger *slog.Logger) {
		logger.With(slog.String("dsn", dsn)).WithGroup("pool").Info("opening")
	})

	if strings.Contains(line, password) {
		t.Errorf("the handler wrote %q, the secret survived", line)
	}
}

// Redaction must never be the reason a log line stops being usable: what is not
// a secret has to come out of the handler as the type it went in as.
func TestHandlerKeepsTheShapeOfEverythingElse(t *testing.T) {
	t.Parallel()

	line := logged(t, func(logger *slog.Logger) {
		logger.WithGroup("connection").Warn("slow",
			slog.Int("port", 5432),
			slog.Bool("ssl", true),
			slog.Group("timing", slog.Int("attempts", 3)))
	})

	var record struct {
		Level      string `json:"level"`
		Message    string `json:"msg"`
		Connection struct {
			Port   int  `json:"port"`
			SSL    bool `json:"ssl"`
			Timing struct {
				Attempts int `json:"attempts"`
			} `json:"timing"`
		} `json:"connection"`
	}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("the handler wrote %q, which is not the record it was given: %v", line, err)
	}

	if record.Level != "WARN" || record.Message != "slow" {
		t.Errorf("record = %+v, want the level and message it was given", record)
	}
	if record.Connection.Port != 5432 || !record.Connection.SSL {
		t.Errorf("record = %+v, want the attributes it was given, still typed", record)
	}
	if record.Connection.Timing.Attempts != 3 {
		t.Errorf("record = %+v, want the nested group to survive grouping", record)
	}
}

// A handler that answered Enabled on its own would either write records the one
// behind it discards, or discard records it wanted.
func TestHandlerAsksTheNextHandlerWhatIsEnabled(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	handler := secret.NewHandler(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelError}))

	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled(info) = true, want the answer of the handler behind it")
	}
	if !handler.Enabled(context.Background(), slog.LevelError) {
		t.Error("Enabled(error) = false, want the answer of the handler behind it")
	}
}

// A value with no secret in it is handed on as it is, so a log meant to be
// machine-readable stays machine-readable.
func TestHandlerLeavesAValueWithNoSecretAsItIs(t *testing.T) {
	t.Parallel()

	line := logged(t, func(logger *slog.Logger) {
		logger.Info("opening", slog.Any("target", struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}{Host: "db.example.com", Port: 5432}))
	})

	var record struct {
		Target struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"target"`
	}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("the handler wrote %q, which is not the record it was given: %v", line, err)
	}

	if record.Target.Host != "db.example.com" || record.Target.Port != 5432 {
		t.Errorf("the handler wrote %q, want the value handed on as the object it is", line)
	}
}

// slog hands an empty group or an empty attribute list to a handler, and a
// decorator that answered them with a new layer would grow one per call for as
// long as the logger lives.
func TestHandlerAddsNoLayerForNothing(t *testing.T) {
	t.Parallel()

	handler := secret.NewHandler(slog.NewJSONHandler(&bytes.Buffer{}, nil))

	if got := handler.WithAttrs(nil); got != slog.Handler(handler) {
		t.Errorf("WithAttrs(nil) = %v, want the handler itself", got)
	}
	if got := handler.WithGroup(""); got != slog.Handler(handler) {
		t.Errorf("WithGroup(\"\") = %v, want the handler itself", got)
	}
}

// failingHandler is a handler that cannot write, which is the state a log file
// on a full disk is in.
type failingHandler struct{ err error }

func (f failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (f failingHandler) Handle(context.Context, slog.Record) error { return f.err }
func (f failingHandler) WithAttrs([]slog.Attr) slog.Handler        { return f }
func (f failingHandler) WithGroup(string) slog.Handler             { return f }

// A write that failed has to be reported as a failure. Redaction sits in the
// middle of the chain and is not allowed to swallow what the end of it says.
func TestHandlerReportsAFailureToWrite(t *testing.T) {
	t.Parallel()

	full := errors.New("no space left on device")
	handler := secret.NewHandler(failingHandler{err: full})

	err := handler.Handle(context.Background(),
		slog.NewRecord(time.Now(), slog.LevelInfo, "opening", 0))

	if !errors.Is(err, full) {
		t.Errorf("Handle(...) = %v, want it to report %v", err, full)
	}
}

// The handler renders an attribute it does not recognise with %+v, which is
// where a struct holding a passphrase with spaces in it used to come out with
// most of the passphrase still in it.
func TestHandlerRedactsAPasswordWithSpacesInIt(t *testing.T) {
	t.Parallel()

	type form struct {
		Host     string
		Password string
	}

	line := logged(t, func(logger *slog.Logger) {
		logger.Info("saving", "form", form{Host: "db.example.com", Password: "correct horse battery"})
	})

	for _, word := range []string{"correct", "horse", "battery"} {
		if strings.Contains(line, word) {
			t.Errorf("the log carries %q of the passphrase: %s", word, line)
		}
	}
	if !strings.Contains(line, "db.example.com") {
		t.Errorf("the log lost the host it was about: %s", line)
	}
}
