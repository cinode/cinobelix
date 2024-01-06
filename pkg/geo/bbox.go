package geo

import "math"

type BBox struct {
	MinLat float64 `yaml:"minLat"`
	MinLon float64 `yaml:"minLon"`
	MaxLat float64 `yaml:"maxLat"`
	MaxLon float64 `yaml:"maxLon"`
}

func (b BBox) leftEdgeLon(x, z int) float64 {
	n := float64(int(1) << z)
	mapX := float64(x) / n
	return (mapX)*360.0 - 180.0
}

func (b BBox) topEdgeLat(y, z int) float64 {
	n := float64(int(1) << z)
	mapY := float64(y) / n
	rad := math.Atan(math.Sinh(math.Pi * (1 - 2*mapY)))
	return rad * 180.0 / math.Pi
}

func (b BBox) ContainsColumn(x, z int) bool {
	minLon := b.leftEdgeLon(x, z)
	maxLon := b.leftEdgeLon(x+1, z)
	return max(minLon, b.MinLon) <= min(maxLon, b.MaxLon)
}

func (b BBox) ContainsRow(y, z int) bool {
	minLat := b.topEdgeLat(y+1, z)
	maxLat := b.topEdgeLat(y, z)
	return max(minLat, b.MinLat) <= min(maxLat, b.MaxLat)
}

func (b BBox) ContainsTile(x, y, z int) bool {
	return b.ContainsColumn(x, z) && b.ContainsRow(y, z)
}
