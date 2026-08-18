package main

import "testing"

func TestResolveDownloadURLUsesCPPComponentTags(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{name: "latest", version: "latest", want: "https://github.com/ClaraCora/CPP/releases/download/corade-latest/corade-linux-amd64"},
		{name: "semantic version", version: "v2.0.3", want: "https://github.com/ClaraCora/CPP/releases/download/corade-v2.0.3/corade-linux-amd64"},
		{name: "component tag", version: "corade-v2.0.3", want: "https://github.com/ClaraCora/CPP/releases/download/corade-v2.0.3/corade-linux-amd64"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := resolveDownloadURL("corade-linux-amd64", testCase.version); got != testCase.want {
				t.Fatalf("resolveDownloadURL() = %q, want %q", got, testCase.want)
			}
		})
	}
}
