package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

func executePrepareClaim(args []string) error {
	flags := flag.NewFlagSet("prepare-claim", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(flags.Output(), claimUsage); flags.PrintDefaults() }
	var o gcsOptions
	var release, configPath, fileServer string
	flags.StringVar(&o.AircraftName, "name", "", "Name of the new UxS in the active GCS Workspace")
	flags.StringVar(&o.UxsArtifact, "beam-artifact", "", "Development override: local target Beam artifact")
	flags.StringVar(&release, "release-url", "", "Release manifest URL (defaults to this wizard release)")
	flags.StringVar(&configPath, "config", "", "Wizard configuration file (defaults beside the executable)")
	flags.StringVar(&fileServer, "file-server", "", "Override the configured file server base URL")
	flags.StringVar(&o.ClaimOutput, "claim-output", "", "Save to a new private file instead of displaying the code")
	flags.BoolVar(&o.NoBrowser, "no-browser", false, "Print the approval URL without opening a browser")
	flags.DurationVar(&o.Wait, "wait-timeout", 10*time.Minute, "Maximum browser approval time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || o.Wait <= 0 {
		return errors.New("provide a positive wait-timeout")
	}
	if o.AircraftName == "" {
		var err error
		o.AircraftName, err = promptText("UxS name", "This name will appear in the active GCS Workspace.", requiredText)
		if err != nil {
			return err
		}
	}
	if o.UxsArtifact == "" {
		var err error
		release, err = configuredClaimRelease(configPath, fileServer, release)
		if err != nil {
			return err
		}
		if release == "" {
			fileServer, err = promptText("File server URL", "Enter the download server base URL. Configure fileServer to remember it.", func(value string) error { _, err := claimReleaseURL(value); return err })
			if err != nil {
				return err
			}
			release, err = claimReleaseURL(fileServer)
			if err != nil {
				return err
			}
		}
	}
	ctx, stop := setupContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	if o.UxsArtifact == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "Fetching the Beam release for UxS provisioning...")
		o.UxsArtifact, err = resolveClaimArtifact(ctx, release, cache)
		if err != nil {
			return err
		}
	}
	beam, err := newLocalAPI("http://127.0.0.1:9091")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Preparing %s in GCS Beam's active Workspace.\nApprove with your Organization Admin account in the browser; this wizard will wait and show the claim code.\n", o.AircraftName)
	i := gcsInstaller{opts: o, beam: beam, emit: progressOutput(), authorize: func(link string) {
		fmt.Fprintln(os.Stderr, "Approve the new UxS here:", link)
		if !o.NoBrowser {
			if err := openBrowser(ctx, link); err != nil {
				fmt.Fprintln(os.Stderr, "Open the approval URL above manually.")
			}
		}
	}}
	return i.prepareClaim(ctx)
}

type claimStatus struct {
	Phase string `json:"phase"`
	URL   string `json:"authorizationUrl"`
	Code  string `json:"claimCode"`
	Name  string `json:"name"`
}

func executeResumeClaim(args []string) error {
	flags := flag.NewFlagSet("resume-claim", flag.ContinueOnError)
	var o gcsOptions
	flags.BoolVar(&o.NoBrowser, "no-browser", false, "Print authorization URLs without opening a browser")
	flags.StringVar(&o.ClaimOutput, "claim-output", "", "Private output file instead of displaying the claim")
	flags.DurationVar(&o.Wait, "wait-timeout", 10*time.Minute, "Maximum remaining approval time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || o.Wait <= 0 {
		return errors.New("provide a positive wait-timeout")
	}
	ctx, stop := setupContext()
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, o.Wait)
	defer cancel()
	beam, err := newLocalAPI("http://127.0.0.1:9091")
	if err != nil {
		return err
	}
	i := gcsInstaller{opts: o, beam: beam, emit: progressOutput(), authorize: func(link string) {
		fmt.Fprintln(os.Stderr, "Approve the pending UxS here:", link)
		if !o.NoBrowser {
			if err := openBrowser(ctx, link); err != nil {
				fmt.Fprintln(os.Stderr, "Open the approval URL above manually.")
			}
		}
	}}
	return i.resumeClaim(ctx)
}
