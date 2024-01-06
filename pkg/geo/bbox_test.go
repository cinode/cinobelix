package geo

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBBoxContainsTile(t *testing.T) {
	bbox := BBox{
		MinLat: 49.0061,
		MinLon: 14.1213,
		MaxLat: 54.8357,
		MaxLon: 24.1533,
	}
	for i, d := range []struct {
		x, y, z  int
		contains bool
	}{
		{8, 5, 4, true},
		{9, 5, 4, true},
		{8, 4, 4, false},
		{8, 6, 4, false},
		{285, 168, 9, true},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			require.Equal(t,
				d.contains,
				bbox.ContainsTile(d.x, d.y, d.z),
			)
		})
	}
}
