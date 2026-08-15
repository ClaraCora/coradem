package kernel

import (
	"testing"

	"github.com/ClaraCora/CPanelde/internal/model"
)

func TestStructuredRoutesRequestGeoData(t *testing.T) {
	rules := []model.CustomRouteRule{{
		Match: model.RouteMatch{Domains: []string{"geosite:google"}, GeoIPs: []string{"google"}},
	}}
	if !NeedsGeoIPRules(rules) {
		t.Fatal("GeoIP categories must request geoip.dat")
	}
	if !NeedsGeoSiteRules(rules) {
		t.Fatal("GeoSite domains must request geosite.dat")
	}
}
