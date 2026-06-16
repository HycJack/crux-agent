package core

import (
	"net/http"
	"time"
)

var (
	SSEClient = &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			ResponseHeaderTimeout: 2 * time.Minute,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
		},
	}

	RegularClient = &http.Client{
		Timeout: 30 * time.Second,
	}
)
