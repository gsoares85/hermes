package ui_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsoares85/hermes/internal/ui"
	"github.com/gsoares85/hermes/internal/version"
)

func TestAppInfoServiceReturnsBuildInformation(t *testing.T) {
	t.Parallel()

	got := ui.NewAppInfoService().Info()
	if want := version.Current(); got != want {
		t.Errorf("Info() = %+v, want %+v", got, want)
	}
}

// The boundary rule is that no secret ever reaches the frontend. A field named
// after a credential on a bound payload is the cheapest way to break it, so the
// shape is asserted instead of trusted.
func TestBoundPayloadCarriesNoCredentialFields(t *testing.T) {
	t.Parallel()

	forbidden := []string{"password", "secret", "token", "credential", "passphrase", "key"}

	payload := reflect.TypeOf(ui.NewAppInfoService().Info())
	for i := range payload.NumField() {
		name := strings.ToLower(payload.Field(i).Name)
		for _, word := range forbidden {
			if strings.Contains(name, word) {
				t.Errorf("field %q looks like a credential and must not cross the boundary", payload.Field(i).Name)
			}
		}
	}
}
