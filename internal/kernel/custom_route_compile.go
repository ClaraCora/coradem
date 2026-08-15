package kernel

import (
	"strings"

	"github.com/ClaraCora/CPanelde/internal/model"
)

func NeedsGeoIPRules(rules []model.CustomRouteRule) bool {
	for _, r := range rules {
		if hasNonBlankRouteValues(r.Match.GeoIPs) {
			return true
		}
		for _, v := range r.Match.IPCIDRs {
			if strings.HasPrefix(v, "geoip:") {
				return true
			}
		}
		for _, v := range r.Match.SourceCIDRs {
			if strings.HasPrefix(v, "geoip:") {
				return true
			}
		}
	}
	return false
}

func hasNonBlankRouteValues(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func NeedsGeoSiteRules(rules []model.CustomRouteRule) bool {
	for _, r := range rules {
		for _, v := range r.Match.Domains {
			if strings.HasPrefix(v, "geosite:") {
				return true
			}
		}
		for _, v := range r.Match.DomainSuffixes {
			if strings.HasPrefix(v, "geosite:") {
				return true
			}
		}
	}
	return false
}
