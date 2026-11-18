package webtools

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

func normalizeDomainFilters(allowed, blocked []string) ([]string, []string, error) {
	if len(allowed) > 0 && len(blocked) > 0 {
		return nil, nil, errors.New("allowed_domains and blocked_domains cannot be used together")
	}
	var err error
	allowed, err = normalizeDomains(allowed)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid allowed_domains: %w", err)
	}
	blocked, err = normalizeDomains(blocked)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid blocked_domains: %w", err)
	}
	return allowed, blocked, nil
}

func normalizeDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(domains))
	seen := make(map[string]struct{}, len(domains))
	for _, value := range domains {
		domain, err := normalizeDomain(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		normalized = append(normalized, domain)
	}
	return normalized, nil
}

func normalizeDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(domain, "[") && strings.HasSuffix(domain, "]") {
		domain = strings.TrimSuffix(strings.TrimPrefix(domain, "["), "]")
	}
	domain = strings.TrimPrefix(domain, ".")
	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", errors.New("domain must not be empty")
	}
	if ip := net.ParseIP(domain); ip != nil {
		return domain, nil
	}
	if strings.ContainsAny(domain, ":/?#@%\\\t\r\n ") {
		return "", fmt.Errorf("invalid domain %q", value)
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("invalid domain %q", value)
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", fmt.Errorf("invalid domain %q", value)
			}
		}
	}
	return domain, nil
}

func validateFetchURL(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("fetch URL must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid fetch URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("fetch URL scheme must be http or https")
	}
	if u.User != nil {
		return "", errors.New("fetch URL must not contain user information")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", errors.New("fetch URL must include a host")
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), nil
}

func domainAllowed(host string, allowed, blocked []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if len(allowed) > 0 {
		for _, domain := range allowed {
			if domainMatches(host, domain) {
				return true
			}
		}
		return false
	}
	for _, domain := range blocked {
		if domainMatches(host, domain) {
			return false
		}
	}
	return true
}

func domainMatches(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func filterSearchItems(items []SearchItem, allowed, blocked []string) []SearchItem {
	if len(allowed) == 0 && len(blocked) == 0 {
		return items
	}
	filtered := make([]SearchItem, 0, len(items))
	for _, item := range items {
		u, err := url.Parse(item.URL)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if domainAllowed(u.Hostname(), allowed, blocked) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
