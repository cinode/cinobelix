package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cinode/cinobelix/pkg/geo"
	"github.com/cinode/go/pkg/blenc"
	"github.com/cinode/go/pkg/cinodefs"
	"github.com/cinode/go/pkg/datastore"
	"github.com/cinode/go/pkg/utilities/golang"

	"gopkg.in/yaml.v3"
)

const defaultConfig = `
urlTemplate: http://cinode-store:8080/tile/{z}/{x}/{y}.png
planetMaxZoom: 9
detailedRegions:
  - name: Poland
    geoBBox:
      minLat: 49.0061
      minLon: 14.1213
      maxLat: 54.8357
      maxLon: 24.1533
    maxZoom: 14
`

func main() {
	ctx := context.Background()

	ds, err := datastore.FromLocation(os.Getenv("CINODE_DATASTORE"))
	if err != nil {
		log.Fatal(err)
	}

	be := blenc.FromDatastore(ds)

	wiOpt := cinodefs.RootWriterInfoString(os.Getenv("CINODE_MAPTILES_WRITERINFO"))

	newWi := false
	if os.Getenv("CINODE_MAPTILES_NEW_WRITERINFO") != "" {
		wiOpt = cinodefs.NewRootDynamicLink()
		newWi = true
	}

	fs, err := cinodefs.New(
		ctx,
		be,
		wiOpt,
	)
	if err != nil {
		log.Fatal(err)
	}

	if newWi {
		fmt.Printf("Created new workspace:\n")
		fmt.Printf("  Entrypoint: %s\n", golang.Must(fs.RootEntrypoint()))
		fmt.Printf("  WriterInfo: %s\n", golang.Must(fs.RootWriterInfo(ctx)))
	}

	cfg := Config{}

	cfgYaml := os.Getenv("CINODE_MAPTILES_CONFIG")
	if len(cfgYaml) == 0 {
		cfgYaml = defaultConfig
	}

	err = yaml.Unmarshal([]byte(cfgYaml), &cfg)
	if err != nil {
		log.Fatal(err)
	}

	gen := tilesGenerator{
		cfg: cfg,
		fs:  fs,
		log: slog.Default(),
	}

	err = gen.Process(ctx)
	if err != nil {
		log.Fatal(err)
	}
}

type DetailedRegionConfig struct {
	Name    string   `yaml:"name"`
	BBox    geo.BBox `yaml:"geoBBox"`
	MaxZoom int      `yaml:"maxZoom"`
}

type Config struct {
	URLTemplate     string                 `yaml:"urlTemplate"`
	PlanetMaxZoom   int                    `yaml:"planetMaxZoom"`
	DetailedRegions []DetailedRegionConfig `yaml:"detailedRegions"`
}

type tilesGenerator struct {
	cfg Config
	fs  cinodefs.FS
	log *slog.Logger
}

func (t *tilesGenerator) fetchTile(
	ctx context.Context,
	x, y, z int,
	log *slog.Logger,
) error {
	url := t.cfg.URLTemplate
	url = strings.ReplaceAll(url, "{x}", fmt.Sprint(x))
	url = strings.ReplaceAll(url, "{y}", fmt.Sprint(y))
	url = strings.ReplaceAll(url, "{z}", fmt.Sprint(z))

	for retry := 0; ctx.Err() == nil; retry++ {
		log := t.log.With("url", url, "retry", retry)

		log.InfoContext(ctx, "Fetching tile started")

		resp, err := http.Get(url)
		if err != nil {
			log.ErrorContext(ctx, "Error downloading tile", "err", err)
			time.Sleep((1 << retry) * time.Second)
			continue
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && retry > 7 {
			resp.Body.Close()
			log.ErrorContext(ctx,
				"Invalid tile server response status code",
				"code", resp.StatusCode,
				"status", resp.Status,
			)
			return fmt.Errorf(
				"tile server responded with status code %d (%s)",
				resp.StatusCode,
				resp.Status,
			)
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			log.ErrorContext(ctx,
				"Incorrect http status code when downloading tile",
				"code", resp.StatusCode,
				"status", resp.Status,
			)
			time.Sleep((1 << retry) * time.Second)
			continue
		}

		_, fName := filepath.Split(url)

		ep, err := t.fs.SetEntryFile(
			ctx,
			[]string{
				fmt.Sprint(z),
				fmt.Sprint(x),
				fName,
			},
			resp.Body,
		)
		resp.Body.Close()
		if err != nil {
			log.ErrorContext(ctx, "Error downloading tile", "err", err)
			time.Sleep((1 << retry) * time.Second)
			continue
		}

		log.InfoContext(ctx, "Tile uploaded to cinode", "bn", ep.BlobName().String())

		return nil
	}

	return ctx.Err()
}

func (t *tilesGenerator) genXLayer(
	ctx context.Context,
	x, z int,
	log *slog.Logger,
) error {
	for y := 0; ctx.Err() == nil && y < 1<<z; y++ {
		// Check if this tile contains any detailed region
		isDetailed := false
		for _, region := range t.cfg.DetailedRegions {
			if z > region.MaxZoom {
				continue
			}

			if region.BBox.ContainsTile(x, y, z) {
				isDetailed = true
				break
			}
		}
		if !isDetailed {
			log.Info("skipping tile", "y", y)
			continue
		}

		// Generate tile
		err := t.fetchTile(
			ctx,
			x, y, z,
			log.With("y", y),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) genZLayer(
	ctx context.Context,
	z int,
	log *slog.Logger,
) error {
	if z <= t.cfg.PlanetMaxZoom {
		return t.genZLayerNoConstraints(ctx, z, log)
	}

	for x := 0; ctx.Err() == nil && x < 1<<z; x++ {
		// Check if this x stripe contains any detailed region
		isDetailed := false
		for _, region := range t.cfg.DetailedRegions {
			if z > region.MaxZoom {
				continue
			}
			if region.BBox.ContainsColumn(x, z) {
				log.Info("column matches detailed region", "x", x, "region", region.Name)
				isDetailed = true
			}
		}
		if !isDetailed {
			// Whole x stripe does not contain any detailed region
			log.Info("skipping column", "x", x)
			continue
		}

		// Generate the layer
		err := t.genXLayer(ctx, x, z, log.With("x", x))
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) genZLayerNoConstraints(
	ctx context.Context,
	z int,
	log *slog.Logger,
) error {
	for x := 0; ctx.Err() == nil && x < 1<<z; x++ {
		// Generate the layer
		err := t.genXLayerNoConstraints(ctx, x, z, log.With("x", x))
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) genXLayerNoConstraints(
	ctx context.Context,
	x, z int,
	log *slog.Logger,
) error {
	for y := 0; ctx.Err() == nil && y < 1<<z; y++ {
		// Generate tile
		err := t.fetchTile(
			ctx,
			x, y, z,
			log.With("y", y),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tilesGenerator) Process(ctx context.Context) error {
	maxZoomLevel := t.cfg.PlanetMaxZoom
	for _, region := range t.cfg.DetailedRegions {
		maxZoomLevel = max(maxZoomLevel, region.MaxZoom)
	}

	// Fetch all tiles
	for z := 0; ctx.Err() == nil && z <= maxZoomLevel; z++ {
		err := t.genZLayer(ctx, z, t.log.With("z", z))
		if err != nil {
			return err
		}
		err = t.fs.Flush(ctx)
		if err != nil {
			return fmt.Errorf("failed to flush the filesystem: %w", err)
		}
	}

	return nil
}
