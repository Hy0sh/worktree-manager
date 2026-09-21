package config

import (
	"strings"
	"testing"
)

func profiled() Project {
	return Project{Profiles: map[string][]string{
		"light": {"db", "backend"},
		"async": {"db", "backend", "worker"},
	}}
}

// No profile is how wtm has always started a stack, and the whole stack is
// what nil means to compose.
func TestServicesForTheEmptyProfileStartsEverything(t *testing.T) {
	got, err := profiled().ServicesFor("")
	if err != nil || got != nil {
		t.Fatalf("ServicesFor(\"\") = %v, %v", got, err)
	}
}

func TestServicesForNamesTheProfilesServices(t *testing.T) {
	got, err := profiled().ServicesFor("light")
	if err != nil {
		t.Fatalf("ServicesFor: %v", err)
	}
	if strings.Join(got, ",") != "db,backend" {
		t.Fatalf("services = %v", got)
	}
}

// A typo must not start the whole stack in silence: that is the one failure
// this resolution exists to prevent.
func TestServicesForRefusesAnUnknownProfile(t *testing.T) {
	_, err := profiled().ServicesFor("ligth")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"ligth", "async", "light"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the error should name the typo and what is on offer, got %q", err)
		}
	}
}

func TestServicesForSaysWhenTheProjectDeclaresNone(t *testing.T) {
	_, err := Project{}.ServicesFor("light")
	if err == nil || !strings.Contains(err.Error(), "declares none") {
		t.Fatalf("err = %v", err)
	}
}

// An empty list would reach compose as no argument at all, which starts
// everything: the opposite of what naming a profile means.
func TestServicesForRefusesAProfileWithNoService(t *testing.T) {
	p := Project{Profiles: map[string][]string{"empty": {}}}
	if _, err := p.ServicesFor("empty"); err == nil {
		t.Fatal("expected an error")
	}
}

// config.json is hand-edited and these land on the docker command line.
func TestServicesForRefusesANonIdentifierService(t *testing.T) {
	p := Project{Profiles: map[string][]string{"bad": {"db", "back end"}}}
	if _, err := p.ServicesFor("bad"); err == nil {
		t.Fatal("expected an error")
	}
}
