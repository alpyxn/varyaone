module github.com/alpyxn/varyaone

go 1.26.0

// Canonical Go toolchain for the project. CI (go-version-file: go.mod) and the
// golang:1.26.6-alpine build image read this; bump here and both follow.
toolchain go1.26.6

require (
	github.com/deepteams/webp v1.2.7
	github.com/go-chi/chi/v5 v5.3.2
	github.com/google/uuid v1.6.0
	github.com/grandcat/zeroconf v1.0.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/jchv/go-webview2 v0.0.0-20260205173254-56598839c808
	github.com/kardianos/service v1.3.0
	github.com/oapi-codegen/runtime v1.7.0
	github.com/signintech/gopdf v0.36.1
	golang.org/x/crypto v0.55.0
	golang.org/x/sync v0.22.0
	golang.org/x/sys v0.47.0
)

require (
	github.com/apapsch/go-jsonmerge/v2 v2.0.0 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/miekg/dns v1.1.27 // indirect
	github.com/phpdave11/gofpdi v1.0.14-0.20211212211723-1f10f9844311 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
