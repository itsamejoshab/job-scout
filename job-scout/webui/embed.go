package webui

import (
	"embed"
	"io/fs"
)

// Dist contains the production frontend bundle.
//
//go:embed dist/*
var embedded embed.FS

var Dist = mustDist()

func mustDist() fs.FS {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}
