package secret

import (
	"context"
	"fmt"
	"log/slog"
)

// Handler decorates another slog.Handler and redacts every record on its way
// through: the message, the attributes of the record, and the attributes bound
// to the logger that emitted it.
//
// It exists because calling Redact at each log statement is a discipline, and a
// discipline is what fails the first time someone is in a hurry. Installed at
// the root of the logger, it makes logging a password an accident that cannot
// happen through this path — including through the framework's own logging,
// which this application does not write and cannot review.
//
// What it is not is a proof. Redact recognises the shapes a connection string
// travels in, so a secret carried in a shape of its own — a field of a struct
// logged whole, a password put in an attribute of its own by name — still gets
// through. The boundary rule remains the one that matters: no type that can
// hold a secret is logged. This is the net under it.
type Handler struct {
	next slog.Handler
}

// NewHandler wraps a handler so that everything reaching it is redacted first.
func NewHandler(next slog.Handler) *Handler {
	return &Handler{next: next}
}

// Enabled defers to the handler behind it. Answering on its own would either
// write records the next handler discards or discard records it wanted.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

// Handle passes the message and every attribute through the redaction and hands
// the result on.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	// Rebuilt rather than mutated: a Record shares its attribute storage with
	// the copies of it a handler may already be holding, and editing in place
	// is how one handler's redaction becomes another's surprise.
	redactedRecord := slog.NewRecord(record.Time, record.Level, Redact(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redactedRecord.AddAttrs(redactAttr(attr))

		return true
	})

	if err := h.next.Handle(ctx, redactedRecord); err != nil {
		return fmt.Errorf("writing the redacted record: %w", err)
	}

	return nil
}

// WithAttrs redacts the attributes before binding them. They are written on
// every record the logger emits, so a secret that gets past here gets past here
// repeatedly.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	return &Handler{next: h.next.WithAttrs(redactAttrs(attrs))}
}

// WithGroup opens the group on the handler behind it. Grouping changes where an
// attribute is written, never whether it is a secret.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &Handler{next: h.next.WithGroup(name)}
}

func redactAttrs(attrs []slog.Attr) []slog.Attr {
	redacted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		redacted[i] = redactAttr(attr)
	}

	return redacted
}

// The key is left alone. It is written by the call site, not by whatever the
// call site was handed, and a key wide enough to hold a DSN is a different bug.
func redactAttr(attr slog.Attr) slog.Attr {
	attr.Value = redactValue(attr.Value)

	return attr
}

func redactValue(value slog.Value) slog.Value {
	// Resolved first, because a value that builds itself on demand is exactly
	// how an expensive connection string avoids being formatted until someone
	// logs it — and the redaction has to see what the handler will write, not
	// the promise of it.
	resolved := value.Resolve()

	switch resolved.Kind() {
	case slog.KindString:
		return slog.StringValue(Redact(resolved.String()))
	case slog.KindGroup:
		return slog.GroupValue(redactAttrs(resolved.Group())...)
	case slog.KindAny:
		return redactAny(resolved)
	default:
		return resolved
	}
}

// redactAny covers everything that is neither a string nor a group: an error, a
// type that prints itself, a slice of connection strings.
//
// It renders the value the way the handler at the end of the chain eventually
// will, and only replaces it when the redaction actually removed something.
// Replacing unconditionally would turn every structured value into text and
// cost the logs their shape; replacing never would let a secret through in any
// container. This pays the cost only where there is a secret to pay it for.
func redactAny(value slog.Value) slog.Value {
	rendered := fmt.Sprintf("%+v", value.Any())

	redactedText := Redact(rendered)
	if redactedText == rendered {
		return value
	}

	return slog.StringValue(redactedText)
}
