// Package version содержит версию сервера. Значение подставляется линкером при
// релизе (-ldflags "-X .../version.Version=..."), по умолчанию "dev".
package version

// Version — текущая версия сервера.
var Version = "dev"
