package web

import (
	"embed"
	"io/fs"
)

//go:embed templates/* static/**/*
var embedFS embed.FS

// TemplateFS 返回模板文件系统
func TemplateFS() fs.FS {
	sub, _ := fs.Sub(embedFS, "templates")
	return sub
}

// StaticFS 返回静态文件系统
func StaticFS() fs.FS {
	sub, _ := fs.Sub(embedFS, "static")
	return sub
}
