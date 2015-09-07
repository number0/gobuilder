package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"

	"github.com/Luzifer/go-openssl"
)

const (
	encryptedGPGDataFile = "/root/gpgkey.asc.enc"
	gpgOwnerTrustData    = "E2FF3D20865D6F9B6AE74ECB7D5420F913246261:6:"
)

type StepSetupGPG struct{}

func (s StepSetupGPG) Name() string { return "GPG-Setup" }

func (s StepSetupGPG) ShouldExecute() bool {
	return config.Inputs.GPGDecryptKey != ""
}

func (s StepSetupGPG) Run() error {
	if _, err := os.Stat(encryptedGPGDataFile); err != nil {
		return fmt.Errorf("Encrypted GPG key not found but required.")
	}

	data, err := ioutil.ReadFile(encryptedGPGDataFile)
	if err != nil {
		return fmt.Errorf("Unable to read encrypted GPG key.")
	}

	o := openssl.New()
	plainKey, err := o.DecryptString(config.Inputs.GPGDecryptKey, string(data))
	if err != nil {
		return fmt.Errorf("Unable to decrypt encrypted GPG key.")
	}

	gpg := exec.Command("/usr/bin/gpg", "--import")
	gpg.Stdin = bytes.NewReader(plainKey)
	if err := gpg.Run(); err != nil {
		return fmt.Errorf("Unable to import GPG key.")
	}

	gpg = exec.Command("/usr/bin/gpg", "--import-ownertrust")
	gpg.Stdin = bytes.NewReader([]byte(gpgOwnerTrustData))
	if err := gpg.Run(); err != nil {
		return fmt.Errorf("Unable to import Owner-Trust.")
	}

	return nil
}
