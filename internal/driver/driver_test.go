package driver_test

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/driver"
)

// Target is the type that carries the password across the seam, so it needs the
// same protection the configuration above it has. Without it, a %v in an error
// or a logger handed the whole struct prints the secret.
func TestNoRenderingOfATargetLeaksThePassword(t *testing.T) {
	t.Parallel()

	target := driver.Target{
		Host: "db.example.com", Port: 5432, Database: "app",
		User: "hermes", Password: "s3cr3t", SSLMode: "require",
	}

	enclosing := struct {
		ID     int
		Target driver.Target
	}{ID: 1, Target: target}

	for name, rendered := range map[string]string{
		"String":          target.String(),
		"%v":              fmt.Sprintf("%v", target),
		"%+v":             fmt.Sprintf("%+v", target),
		"slog value":      target.LogValue().String(),
		"inside a struct": fmt.Sprintf("%+v", enclosing),
	} {
		if strings.Contains(rendered, "s3cr3t") {
			t.Errorf("%s leaked the password: %s", name, rendered)
		}
	}
}

func TestATargetIsALogValuer(t *testing.T) {
	t.Parallel()

	var value any = driver.Target{}
	if _, ok := value.(slog.LogValuer); !ok {
		t.Fatal("Target does not implement slog.LogValuer, so a logger would print its fields")
	}
}

func TestATargetStillShowsWhatIsNotSecret(t *testing.T) {
	t.Parallel()

	target := driver.Target{Host: "db.example.com", Port: 5432, Database: "app", User: "hermes"}

	for _, want := range []string{"db.example.com", "5432", "app", "hermes"} {
		if !strings.Contains(target.String(), want) {
			t.Errorf("String() = %q, want it to carry %q", target.String(), want)
		}
	}
}
