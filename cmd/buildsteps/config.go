package main

import "github.com/Luzifer/gobuilder/buildconfig"

type configData struct {
	Inputs struct {
		Repository    string `env:"REPO" default:"" description:"Repository to build"`
		Commit        string `env:"COMMIT" default:"" description:"Commit to build from repository"`
		ForceBuild    bool   `env:"FORCE_BUILD" default:"false" description:"Enforce building even when already built"`
		GPGDecryptKey string `env:"GPG_DECRYPT_KEY" default:"" description:"Key to decrypt the GPG key"`
	}

	BuildConfig buildconfig.BuildConfig
}
