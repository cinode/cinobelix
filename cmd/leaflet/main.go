package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/cinode/go/pkg/blenc"
	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/datastore"
	"github.com/google/go-github/v57/github"
)

func main() {
	ctx := context.Background()
	gh := github.NewClient(nil)

	ds, err := datastore.FromLocation(os.Getenv("CINODE_DATASTORE"))
	if err != nil {
		log.Fatal(err)
	}

	fs, err := cinodefs.New(
		ctx,
		blenc.FromDatastore(ds),
		cinodefs.RootWriterInfoString(os.Getenv("CINODE_LEAFLET_WRITERINFO")),
	)
	if err != nil {
		log.Fatal(err)
	}

	err = processLeaflet(ctx, gh, fs)
	if err != nil {
		log.Fatal(err)
	}

	ep, err := fs.RootEntrypoint()
	if err != nil {
		log.Fatal(err)
	}

	wi, err := fs.RootWriterInfo(ctx)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Upload done\nEntrypoint: %s\nWriterInfo: %s\n",
		ep.String(),
		wi.String(),
	)
}

func processLeaflet(
	ctx context.Context,
	gh *github.Client,
	fs cinodefs.FS,
) error {
	release, _, err := gh.Repositories.GetLatestRelease(ctx, "Leaflet", "Leaflet")
	if err != nil {
		return err
	}

	latestVersion := release.GetName()

	currentVersion, err := getCurrentVersion(ctx, fs)
	if err != nil {
		return err
	}

	if currentVersion == latestVersion {
		fmt.Println("version is up-to-date, skipping upload")
		return nil
	}

	for _, asset := range release.Assets {
		if asset.GetName() == "leaflet.zip" {
			return processLeafletZip(
				ctx,
				latestVersion,
				asset.GetBrowserDownloadURL(),
				fs,
			)
		}
	}

	return errors.New("did not find leaflet.zip asset in the release")
}

var versionPath = []string{".cinobelix", "version"}

func processLeafletZip(
	ctx context.Context,
	version string,
	url string,
	fs cinodefs.FS,
) error {
	res, err := http.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	zipData, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	zipFile, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}

	requiredFiles := map[string]bool{
		"dist/leaflet.css": true,
		"dist/leaflet.js":  true,
	}
	for _, file := range zipFile.File {
		if !strings.HasPrefix(file.Name, "dist/") {
			return fmt.Errorf("invalid zip archive - entry %s does not start with dist/", file.Name)
		}
		delete(requiredFiles, file.Name)
	}
	for file := range requiredFiles {
		return fmt.Errorf("invalid zip archive - missing entry %s", file)
	}

	for _, file := range zipFile.File {
		if strings.HasSuffix(file.Name, "/") {
			continue
		}

		path := strings.Split(
			strings.TrimPrefix(file.Name, "dist/"),
			"/",
		)

		data, err := file.Open()
		if err != nil {
			return err
		}

		ep, err := fs.SetEntryFile(ctx, path, data)
		data.Close()
		if err != nil {
			return err
		}

		fmt.Printf("uploaded %s: mime type %s\n", file.Name, ep.MimeType())
	}

	_, err = fs.SetEntryFile(ctx, versionPath, strings.NewReader(version))
	if err != nil {
		return err
	}

	err = fs.Flush(ctx)
	if err != nil {
		return err
	}

	return nil
}

func getCurrentVersion(ctx context.Context, fs cinodefs.FS) (string, error) {
	rc, err := fs.OpenEntryData(ctx, versionPath)
	if errors.Is(err, cinodefs.ErrEntryNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
