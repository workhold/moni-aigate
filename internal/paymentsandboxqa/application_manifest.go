package paymentsandboxqa

import (
	"os"
	"path/filepath"
)

func applicationWriteSecret(dir, name string, raw []byte) (applicationSecretFile, error) {
	if _, err := writeSecret(dir, name, raw); err != nil {
		return applicationSecretFile{}, err
	}
	return applicationSecretFile{Name: name, SHA256: applicationSHA(raw)}, nil
}

func (e *ApplicationEnvironment) writeApplicationManifests(proof applicationProof, canonical []byte) error {
	for _, component := range []string{"application", "worker"} {
		dir := filepath.Join(e.directory, component)
		if os.Mkdir(dir, 0700) != nil {
			return stage("application_component_directory")
		}
		nonce := e.appNonce
		if component == "worker" {
			nonce = e.workerNonce
		}
		ref, err := applicationWriteSecret(dir, "nonce", []byte(encoded(nonce)))
		if err != nil {
			return err
		}
		manifest := applicationManifest{ManifestVersion: 1, Component: component, Proof: proof, ProofSHA256: applicationSHA(canonical), NonceFile: ref, Keys: []applicationMaterial{{ID: "qa-key", MaterialBase64URL: encoded(e.key)}}}
		var wire any = manifest
		if component == "worker" {
			dsn, err := e.RuntimeDSN("worker")
			if err != nil {
				return err
			}
			dsnRef, err := applicationWriteSecret(dir, "runtime_dsn", []byte(dsn))
			if err != nil {
				return err
			}
			tokenRef, err := applicationWriteSecret(dir, "api_token", []byte(encoded(e.api)))
			if err != nil {
				return err
			}
			wire = applicationWorkerManifest{applicationManifest: manifest, RuntimeDSNFile: dsnRef, APITokenFile: tokenRef}
		}
		raw, err := applicationCanonical(wire)
		if err != nil {
			return err
		}
		if _, err = applicationWriteSecret(dir, "manifest.json", raw); err != nil {
			return err
		}
		path := filepath.Join(dir, "manifest.json")
		if component == "application" {
			e.ApplicationManifestPath = path
		} else {
			e.WorkerManifestPath = path
		}
	}
	return nil
}
