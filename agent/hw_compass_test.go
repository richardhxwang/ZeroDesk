package main

import (
	"math"
	"math/rand"
	"testing"
)

// A distorted sphere (offset + scaled axes) must fit back to a unit-ish field.
func TestFitEllipsoid(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	off := vec3{0.12, -0.3, 0.05}
	var samples []vec3
	for range 600 {
		v := vec3{r.NormFloat64(), r.NormFloat64(), r.NormFloat64()}
		v.normalize(1e-9)
		v = vec3{v[0] * 0.5, v[1] * 0.42, v[2] * 0.58}
		samples = append(samples, vec3{v[0] + off[0], v[1] + off[1], v[2] + off[2]})
	}
	cal, hint := fitEllipsoid(samples)
	if hint != "" {
		t.Fatalf("hint %q", hint)
	}
	for i := range 3 {
		if math.Abs(cal.Offset[i]-off[i]) > 1e-3 {
			t.Fatalf("offset %v want %v", cal.Offset, off)
		}
	}
	var lo, hi = 10.0, 0.0
	for _, s := range samples {
		c := cal.apply(s)
		n := math.Sqrt(c.dot(c))
		lo, hi = math.Min(lo, n), math.Max(hi, n)
	}
	if hi/lo > 1.01 {
		t.Fatalf("corrected field not spherical: %v..%v", lo, hi)
	}
	if hint2 := func() string { _, h := fitEllipsoid(samples[:50]); return h }(); hint2 != "rotate" {
		t.Fatalf("few samples: %q", hint2)
	}
}

// Flat, screen up, top edge towards magnetic north -> heading 0.
func TestCompassPoseNorth(t *testing.T) {
	p := calcCompassPose(vec3{0, 0, 9.8}, vec3{0, 30, -40}, true)
	if !p.HeadingValid || math.Abs(p.Heading) > 0.5 && math.Abs(p.Heading-360) > 0.5 {
		t.Fatalf("heading %v", p.Heading)
	}
	p = calcCompassPose(vec3{0, 0, 9.8}, vec3{30, 0, -40}, true) // north to the right -> facing west
	if math.Abs(p.Heading-270) > 0.5 {
		t.Fatalf("heading %v want 270", p.Heading)
	}
}
