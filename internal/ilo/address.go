package ilo

import (
	"net"
	"strconv"
	"strings"
)

func ParseAddress(s string) (host string, httpsPort uint16, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 443, nil
	}
	host = s
	httpsPort = 443

	if strings.HasPrefix(s, "[") {
		h, p, splitErr := net.SplitHostPort(s)
		if splitErr == nil {
			port, err := parsePort(p)
			if err != nil {
				return "", 0, err
			}
			return h, port, nil
		}
		return strings.Trim(s, "[]"), 443, nil
	}

	if strings.Count(s, ":") == 1 {
		h, p, splitErr := net.SplitHostPort(s)
		if splitErr != nil {
			h, p, _ = strings.Cut(s, ":")
		}
		port, err := parsePort(p)
		if err != nil {
			return "", 0, err
		}
		host = h
		httpsPort = port
	}
	return host, httpsPort, nil
}

func parsePort(value string) (uint16, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	return uint16(port), err
}
