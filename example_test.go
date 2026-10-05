package airstrings_test

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"golang.org/x/text/language"

	"github.com/symbionix-sl/airstrings-sdk-go"
)

func Example() {
	client, err := airstrings.New(airstrings.Config{
		OrganizationID: "org_a1b2c3d4e5f6",
		ProjectID:      "proj_a1b2c3d4e5f6",
		EnvironmentID:  "env_a1b2c3d4e5f6",
		PublicKeys:     []string{"cubn9XZIRd+D8xwC3u9CLiAIJlvraVsK8foWPRuWtcQ="},
		Locales:        []string{"en", "it"},
		OnError:        func(err error) { log.Printf("airstrings: %v", err) },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.WaitReady(ctx)

	l := client.Localizer("it", "")
	fmt.Println(l.T("greeting"))
	fmt.Println(l.Format("items.count", map[string]any{"count": 3}))
}

//go:embed testdata/seed/*.json
var bundles embed.FS

// Ship the bundles written by `airstrings bundles pull` inside the binary so a
// cold start with no network serves real strings.
func ExampleConfig_seed() {
	seed, err := fs.Sub(bundles, "testdata/seed")
	if err != nil {
		log.Fatal(err)
	}
	client, err := airstrings.New(airstrings.Config{
		OrganizationID: "org_a1b2c3d4e5f6",
		ProjectID:      "proj_a1b2c3d4e5f6",
		EnvironmentID:  "env_a1b2c3d4e5f6",
		PublicKeys:     []string{"cubn9XZIRd+D8xwC3u9CLiAIJlvraVsK8foWPRuWtcQ="},
		Locales:        []string{"en", "it"},
		Seed:           seed,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	fmt.Println(client.Localizer("en", "").T("greeting"))
}

// Pick the locale per request and pin one bundle revision for the whole response.
func ExampleClient_Localizer() {
	var client *airstrings.Client // created once at startup with airstrings.New

	supported := []language.Tag{language.English, language.Italian}
	matcher := language.NewMatcher(supported)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, i := language.MatchStrings(matcher, r.Header.Get("Accept-Language"))
		l := client.Localizer(supported[i].String(), r.Header.Get("X-User-ID"))
		fmt.Fprintln(w, l.T("greeting"))
	})
}

func ExampleLocalizer_Format() {
	var client *airstrings.Client

	l := client.Localizer("en", "")
	// "{count, plural, one {# item} other {# items}}" with count 1200 → "1,200 items".
	fmt.Println(l.Format("items.count", map[string]any{"count": 1200}))
}
