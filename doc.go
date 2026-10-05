// Package airstrings is the Go server SDK for AirStrings: it fetches
// Ed25519-signed string bundles from the AirStrings CDN, verifies them, keeps
// them fresh in the background and serves them lock-free.
//
// Create one Client per process, then a Localizer per request:
//
//	client, err := airstrings.New(airstrings.Config{
//		OrganizationID: "org_xxx",
//		ProjectID:      "proj_xxx",
//		EnvironmentID:  "env_xxx",
//		PublicKeys:     []string{"BASE64_ED25519_PUBLIC_KEY"},
//		Locales:        []string{"en", "it"},
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer client.Close()
//
//	l := client.Localizer("it", userID)
//	fmt.Println(l.T("greeting"))
//	fmt.Println(l.Format("items.count", map[string]any{"count": 3}))
//
// Bundles that fail verification are never served: the Client keeps the last
// good bundle, and with nothing loaded T and Format return the key name.
package airstrings
