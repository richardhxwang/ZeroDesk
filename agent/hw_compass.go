package main

import (
	"bufio"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Compass: BMI270 (accelerometer, gyroscope) and BMM150 (magnetometer) from
// IIO sysfs, readable by every user. Pose, heading and the ellipsoid
// calibration are ported from CardputerZero/Compass (MIT,
// src/models/compass_pose.cpp, src/models/calibration_model.cpp); the
// calibration file is the original's (/tmp/Compass/calibration.conf, kept for
// the current boot), so both apps share one calibration.

type vec3 [3]float64

func (a vec3) dot(b vec3) float64   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a vec3) sub(b vec3) vec3      { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) scale(f float64) vec3 { return vec3{a[0] * f, a[1] * f, a[2] * f} }
func (a vec3) cross(b vec3) vec3 {
	return vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func (a vec3) finite() bool {
	for _, v := range a {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
func (a *vec3) normalize(min float64) bool {
	n := math.Sqrt(a.dot(*a))
	if math.IsNaN(n) || n < min {
		return false
	}
	*a = a.scale(1 / n)
	return true
}

type mat3 [3][3]float64

func (m mat3) mul(v vec3) vec3 {
	var r vec3
	for i := range 3 {
		for j := range 3 {
			r[i] += m[i][j] * v[j]
		}
	}
	return r
}

func (m mat3) det() float64 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) - m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) + m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
}

func (m mat3) inverse() (mat3, bool) {
	d := m.det()
	if math.IsNaN(d) || math.Abs(d) < 1e-10 {
		return mat3{}, false
	}
	return mat3{
		{(m[1][1]*m[2][2] - m[1][2]*m[2][1]) / d, (m[0][2]*m[2][1] - m[0][1]*m[2][2]) / d, (m[0][1]*m[1][2] - m[0][2]*m[1][1]) / d},
		{(m[1][2]*m[2][0] - m[1][0]*m[2][2]) / d, (m[0][0]*m[2][2] - m[0][2]*m[2][0]) / d, (m[0][2]*m[1][0] - m[0][0]*m[1][2]) / d},
		{(m[1][0]*m[2][1] - m[1][1]*m[2][0]) / d, (m[0][1]*m[2][0] - m[0][0]*m[2][1]) / d, (m[0][0]*m[1][1] - m[0][1]*m[1][0]) / d},
	}, true
}

// sensor frames -> screen frame (x right, y towards the top edge, z out of the screen)
func bmi270ToScreen(s vec3) vec3 { return vec3{s[1], -s[0], s[2]} }
func bmm150ToScreen(s vec3) vec3 { return vec3{s[1], s[0], -s[2]} }

type compassPose struct {
	Pitch, Roll, BubbleX, BubbleY, Heading float64
	HeadingValid                           bool
}

func calcCompassPose(accel, mag vec3, haveMag bool) compassPose {
	var p compassPose
	if !accel.finite() {
		return p
	}
	const bubbleRange = 18.0
	pitch := math.Atan2(accel[1], math.Hypot(accel[0], accel[2]))
	roll := math.Atan2(accel[0], math.Hypot(accel[1], accel[2]))
	p.Pitch, p.Roll = pitch*180/math.Pi, roll*180/math.Pi
	p.BubbleX = math.Max(-1, math.Min(1, p.Roll/bubbleRange))
	p.BubbleY = math.Max(-1, math.Min(1, -p.Pitch/bubbleRange))
	if !haveMag || !mag.finite() {
		return p
	}
	up := accel
	if !up.normalize(1e-5) {
		return p
	}
	top := vec3{0, 1, 0}
	forward := top.sub(up.scale(top.dot(up)))
	if !forward.normalize(0.05) {
		return p
	}
	north := mag.sub(up.scale(mag.dot(up)))
	if !north.normalize(1e-5) {
		return p
	}
	right := forward.cross(up)
	if !right.normalize(1e-5) {
		return p
	}
	if up[2] < 0 {
		right = right.scale(-1)
	}
	h := math.Mod(math.Atan2(-north.dot(right), north.dot(forward))*180/math.Pi, 360)
	if h < 0 {
		h += 360
	}
	p.Heading, p.HeadingValid = h, !math.IsNaN(h)
	return p
}

// ---- calibration (ellipsoid fit, original v2 algorithm) ----

type compassCal struct {
	Valid  bool
	Offset vec3
	M      mat3 // symmetric correction matrix
}

func (c compassCal) apply(m vec3) vec3 {
	if !c.Valid {
		return m
	}
	return c.M.mul(m.sub(c.Offset))
}

func (c compassCal) ok() bool {
	if !c.Valid || !c.Offset.finite() {
		return false
	}
	for _, r := range c.M {
		if !vec3(r).finite() {
			return false
		}
	}
	m := c.M
	return m[0][0] > 1e-8 && m[0][0]*m[1][1]-m[0][1]*m[1][0] > 1e-8 && m.det() > 1e-8
}

func compassCalPath() string {
	if p := os.Getenv("COMPASS_CALIBRATION_PATH"); p != "" {
		return p
	}
	if d := os.Getenv("COMPASS_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "calibration.conf")
	}
	return "/tmp/Compass/calibration.conf"
}

func loadCompassCal() compassCal {
	f, err := os.Open(compassCalPath())
	if err != nil {
		return compassCal{}
	}
	defer f.Close()
	v := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		k, val, ok := strings.Cut(line, "=")
		if line == "" || line[0] == '#' || !ok {
			continue
		}
		if x, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			v[strings.TrimSpace(k)] = x
		}
	}
	ver, has := v["version"]
	if !has {
		ver = 1
	}
	if ver != 1 && ver != 2 {
		return compassCal{}
	}
	for _, k := range []string{"mag_offset_x", "mag_offset_y", "mag_offset_z", "mag_scale_x", "mag_scale_y", "mag_scale_z"} {
		if _, ok := v[k]; !ok {
			return compassCal{}
		}
	}
	c := compassCal{Valid: true, Offset: vec3{v["mag_offset_x"], v["mag_offset_y"], v["mag_offset_z"]}}
	xy, xz, yz := v["mag_cross_xy"], v["mag_cross_xz"], v["mag_cross_yz"] // version 1: diagonal only
	c.M = mat3{{v["mag_scale_x"], xy, xz}, {xy, v["mag_scale_y"], yz}, {xz, yz, v["mag_scale_z"]}}
	if !c.ok() {
		return compassCal{}
	}
	return c
}

func saveCompassCal(c compassCal) error {
	p := compassCalPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	g := func(x float64) string { return strconv.FormatFloat(float64(float32(x)), 'g', 6, 32) }
	s := "# M5CardputerZero Compass calibration\n# Magnetic offsets are stored in gauss\nversion=2\n" +
		"mag_offset_x=" + g(c.Offset[0]) + "\nmag_offset_y=" + g(c.Offset[1]) + "\nmag_offset_z=" + g(c.Offset[2]) + "\n" +
		"mag_scale_x=" + g(c.M[0][0]) + "\nmag_scale_y=" + g(c.M[1][1]) + "\nmag_scale_z=" + g(c.M[2][2]) + "\n" +
		"mag_cross_xy=" + g(c.M[0][1]) + "\nmag_cross_xz=" + g(c.M[0][2]) + "\nmag_cross_yz=" + g(c.M[1][2]) + "\n"
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(s), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func solveLinear(a [][]float64) ([]float64, bool) {
	n := len(a)
	scale := 0.0
	for _, r := range a {
		for j := range n {
			scale = math.Max(scale, math.Abs(r[j]))
		}
	}
	if scale <= 0 || math.IsNaN(scale) {
		return nil, false
	}
	for c := range n {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		if math.Abs(a[p][c]) <= scale*1e-10 {
			return nil, false
		}
		a[p], a[c] = a[c], a[p]
		d := a[c][c]
		for k := c; k <= n; k++ {
			a[c][k] /= d
		}
		for r := range n {
			if r == c {
				continue
			}
			f := a[r][c]
			for k := c; k <= n; k++ {
				a[r][k] -= f * a[c][k]
			}
		}
	}
	x := make([]float64, n)
	for r := range n {
		x[r] = a[r][n]
		if math.IsNaN(x[r]) || math.IsInf(x[r], 0) {
			return nil, false
		}
	}
	return x, true
}

func symEigen(m mat3) (vec3, mat3, bool) {
	v := mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	for range 48 {
		p, q := 0, 1
		for r := range 3 {
			for c := r + 1; c < 3; c++ {
				if math.Abs(m[r][c]) > math.Abs(m[p][q]) {
					p, q = r, c
				}
			}
		}
		ds := math.Max(1, math.Max(math.Abs(m[0][0]), math.Max(math.Abs(m[1][1]), math.Abs(m[2][2]))))
		if math.Abs(m[p][q]) <= ds*1e-12 {
			return vec3{m[0][0], m[1][1], m[2][2]}, v, true
		}
		ang := 0.5 * math.Atan2(2*m[p][q], m[q][q]-m[p][p])
		c, s := math.Cos(ang), math.Sin(ang)
		app, aqq, apq := m[p][p], m[q][q], m[p][q]
		for i := range 3 {
			if i == p || i == q {
				continue
			}
			aip, aiq := m[i][p], m[i][q]
			m[i][p], m[p][i] = c*aip-s*aiq, c*aip-s*aiq
			m[i][q], m[q][i] = s*aip+c*aiq, s*aip+c*aiq
		}
		m[p][p] = c*c*app - 2*s*c*apq + s*s*aqq
		m[q][q] = s*s*app + 2*s*c*apq + c*c*aqq
		m[p][q], m[q][p] = 0, 0
		for r := range 3 {
			vp, vq := v[r][p], v[r][q]
			v[r][p], v[r][q] = c*vp-s*vq, s*vp+c*vq
		}
	}
	return vec3{}, v, false
}

const (
	calAutoFinish = 20 * time.Second
	calRetry      = 2 * time.Second
	calMinSamples = 120
	calMaxSamples = 2048
	calMinSpan    = 0.40
	calCoverage   = 0.8
)

// fitEllipsoid returns the calibration or a hint for the user (English key
// the front end translates).
func fitEllipsoid(samples []vec3) (compassCal, string) {
	const keepRotating = "rotate"
	if len(samples) < calMinSamples {
		return compassCal{}, keepRotating
	}
	lo, hi := samples[0], samples[0]
	for _, s := range samples {
		for i := range 3 {
			lo[i], hi[i] = math.Min(lo[i], s[i]), math.Max(hi[i], s[i])
		}
	}
	span := hi.sub(lo)
	if span[0] < calMinSpan || span[1] < calMinSpan || span[2] < calMinSpan {
		return compassCal{}, keepRotating
	}
	center0 := vec3{(hi[0] + lo[0]) / 2, (hi[1] + lo[1]) / 2, (hi[2] + lo[2]) / 2}
	sc := (span[0] + span[1] + span[2]) / 6
	if sc <= 1e-6 {
		return compassCal{}, keepRotating
	}
	normal := make([][]float64, 9)
	for i := range normal {
		normal[i] = make([]float64, 10)
	}
	for _, s := range samples {
		x, y, z := (s[0]-center0[0])/sc, (s[1]-center0[1])/sc, (s[2]-center0[2])/sc
		t := [9]float64{x * x, y * y, z * z, 2 * x * y, 2 * x * z, 2 * y * z, 2 * x, 2 * y, 2 * z}
		for r := range 9 {
			for c := range 9 {
				normal[r][c] += t[r] * t[c]
			}
			normal[r][9] += t[r]
		}
	}
	k, ok := solveLinear(normal)
	if !ok {
		return compassCal{}, keepRotating
	}
	q := mat3{{k[0], k[3], k[4]}, {k[3], k[1], k[5]}, {k[4], k[5], k[2]}}
	qi, ok := q.inverse()
	if !ok {
		return compassCal{}, keepRotating
	}
	center := qi.mul(vec3{k[6], k[7], k[8]}).scale(-1)
	norm := 1 + center.dot(q.mul(center))
	if norm <= 1e-8 || math.IsNaN(norm) {
		return compassCal{}, keepRotating
	}
	for r := range 3 {
		for c := range 3 {
			q[r][c] /= norm
		}
	}
	ev, evec, ok := symEigen(q)
	if !ok {
		return compassCal{}, keepRotating
	}
	mn, mx := math.Min(ev[0], math.Min(ev[1], ev[2])), math.Max(ev[0], math.Max(ev[1], ev[2]))
	if mn <= 1e-8 || math.IsNaN(mx) || math.Sqrt(mx/mn) > 10 {
		return compassCal{}, "metal"
	}
	vol := math.Pow(ev[0]*ev[1]*ev[2], -1.0/6)
	var corr mat3
	for e := range 3 {
		ce := math.Sqrt(ev[e]) * vol
		for r := range 3 {
			for c := range 3 {
				corr[r][c] += ce * evec[r][e] * evec[c][e]
			}
		}
	}
	cal := compassCal{Valid: true, Offset: vec3{center0[0] + sc*center[0], center0[1] + sc*center[1], center0[2] + sc*center[2]}, M: corr}
	if !cal.ok() {
		return compassCal{}, keepRotating
	}
	var mask uint8
	sum := 0.0
	norms := make([]float64, 0, len(samples))
	for _, s := range samples {
		c := cal.apply(s)
		n := math.Sqrt(c.dot(c))
		if n <= 1e-8 || math.IsNaN(n) {
			return compassCal{}, keepRotating
		}
		norms = append(norms, n)
		sum += n
		o := 0
		for i := range 3 {
			if c[i] >= 0 {
				o |= 1 << i
			}
		}
		mask |= 1 << o
	}
	mean := sum / float64(len(norms))
	res := 0.0
	for _, n := range norms {
		d := n/mean - 1
		res += d * d
	}
	res = math.Sqrt(res / float64(len(norms)))
	if mask != 0xff {
		return compassCal{}, "turn"
	}
	if mean < 0.05 || mean > 2 || res > 0.12 {
		return compassCal{}, "metal"
	}
	return cal, ""
}

// calProgress mirrors the original's progress: min of axis coverage, sample
// count and octant coverage.
func calProgress(samples []vec3, count int) float64 {
	if len(samples) == 0 {
		return 0
	}
	lo, hi := samples[0], samples[0]
	for _, s := range samples {
		for i := range 3 {
			lo[i], hi[i] = math.Min(lo[i], s[i]), math.Max(hi[i], s[i])
		}
	}
	span := hi.sub(lo)
	cov := math.Min(span[0], math.Min(span[1], span[2])) / calCoverage
	sp := float64(count) / calMinSamples
	var mask uint8
	for _, s := range samples {
		o := 0
		for i := range 3 {
			if s[i] >= (hi[i]+lo[i])/2 {
				o |= 1 << i
			}
		}
		mask |= 1 << o
	}
	n := 0
	for m := mask; m != 0; m >>= 1 {
		n += int(m & 1)
	}
	return math.Max(0, math.Min(1, math.Min(cov, math.Min(sp, float64(n)/8))))
}

// ---- the backend ----

type compassState struct {
	Available    bool       `json:"available"`
	Magnetometer bool       `json:"magnetometer"`
	Error        string     `json:"error,omitempty"`
	Accel        [3]float64 `json:"accel"` // m/s², screen frame
	Gyro         [3]float64 `json:"gyro"`  // rad/s
	Mag          [3]float64 `json:"mag"`   // µT, calibrated, screen frame
	Heading      float64    `json:"heading"`
	HeadingValid bool       `json:"heading_valid"`
	Pitch        float64    `json:"pitch"`
	Roll         float64    `json:"roll"`
	BubbleX      float64    `json:"bubble_x"`
	BubbleY      float64    `json:"bubble_y"`
	Calibrated   bool       `json:"calibrated"`
	Calibration  struct {
		Running  bool    `json:"running"`
		Progress float64 `json:"progress"`
		Hint     string  `json:"hint,omitempty"`
		Done     bool    `json:"done,omitempty"`
	} `json:"calibration"`
}

type compassBackend struct {
	mu       sync.Mutex
	hub      *hwHub
	stop     chan struct{}
	cal      compassCal
	calMtime time.Time
	last     compassState
	lastHead float64
	haveHead bool
	// calibration capture
	running  bool
	samples  []vec3
	next     int
	count    int
	started  time.Time
	retrying bool
	hint     string
	done     bool
}

func hwCompass(mux *http.ServeMux) func() {
	b := &compassBackend{cal: loadCompassCal()}
	b.hub = newHWHub(b.startLoop, b.stopLoop)
	b.last = b.read()
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		hwJSON(w, http.StatusOK, b.read())
	})
	mux.HandleFunc("GET /api/stream", func(w http.ResponseWriter, r *http.Request) {
		b.hub.serve(w, r, func() (string, any) { return "state", b.read() })
	})
	mux.HandleFunc("POST /api/calibration/start", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.running, b.samples, b.next, b.count, b.started, b.retrying, b.hint, b.done = true, b.samples[:0], 0, 0, time.Now(), false, "rotate", false
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/calibration/cancel", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.running, b.done = false, false
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return b.stopLoop
}

func (b *compassBackend) startLoop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stop != nil {
		return
	}
	stop := make(chan struct{})
	b.stop = stop
	go func() {
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				b.hub.publish("state", b.read())
			}
		}
	}()
}

func (b *compassBackend) stopLoop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stop != nil {
		close(b.stop)
		b.stop = nil
	}
}

func readAxes(dir, kind string, scale float64) (vec3, error) {
	var v vec3
	for i, a := range []string{"x", "y", "z"} {
		x, err := sysFloat(filepath.Join(dir, fmt.Sprintf("in_%s_%s_raw", kind, a)))
		if err != nil {
			return v, err
		}
		v[i] = x * scale
	}
	return v, nil
}

func (b *compassBackend) read() compassState {
	var s compassState
	imu := iioDevice("bmi270")
	if imu == "" {
		s.Error = "no-imu"
		return b.finish(s, vec3{}, false)
	}
	as, err1 := sysFloat(imu + "/in_accel_scale")
	gs, err2 := sysFloat(imu + "/in_anglvel_scale")
	accel, err3 := readAxes(imu, "accel", as)
	gyro, err4 := readAxes(imu, "anglvel", gs)
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		s.Error = "read"
		return b.finish(s, vec3{}, false)
	}
	s.Available = true
	accel, gyro = bmi270ToScreen(accel), bmi270ToScreen(gyro)
	s.Accel, s.Gyro = accel, gyro
	var raw vec3
	have := false
	if mag := iioDevice("bmm150"); mag != "" {
		if ms, err := sysFloat(mag + "/in_magn_scale"); err == nil {
			if v, err := readAxes(mag, "magn", ms); err == nil && math.Sqrt(v.dot(v)) > 1e-5 {
				raw, have = v, true
			}
		}
	}
	s.Magnetometer = have
	return b.finish(s, raw, have)
}

func (b *compassBackend) finish(s compassState, raw vec3, have bool) compassState {
	b.mu.Lock()
	defer b.mu.Unlock()
	// the device app may have saved a new calibration
	if st, err := os.Stat(compassCalPath()); err == nil && !st.ModTime().Equal(b.calMtime) {
		b.calMtime = st.ModTime()
		b.cal = loadCompassCal()
	}
	if have {
		mag := bmm150ToScreen(b.cal.apply(raw)).scale(100) // gauss -> µT
		s.Mag = mag
		p := calcCompassPose(vec3(s.Accel), mag, true)
		s.Pitch, s.Roll, s.BubbleX, s.BubbleY = p.Pitch, p.Roll, p.BubbleX, p.BubbleY
		if p.HeadingValid {
			b.lastHead, b.haveHead = p.Heading, true
		}
		s.Heading, s.HeadingValid = b.lastHead, b.haveHead
		if b.running {
			if len(b.samples) < calMaxSamples {
				b.samples = append(b.samples, raw)
			} else {
				b.samples[b.next] = raw
				b.next = (b.next + 1) % calMaxSamples
			}
			b.count++
			wait := calAutoFinish
			if b.retrying {
				wait = calRetry
			}
			if time.Since(b.started) >= wait {
				if cal, hint := fitEllipsoid(b.samples); hint == "" {
					if err := saveCompassCal(cal); err == nil {
						b.cal, b.running, b.done, b.hint = cal, false, true, ""
						if st, err := os.Stat(compassCalPath()); err == nil {
							b.calMtime = st.ModTime()
						}
					} else {
						b.hint = "save"
						b.started, b.retrying = time.Now(), true
					}
				} else {
					b.hint = hint
					b.started, b.retrying = time.Now(), true
				}
			}
		}
	} else if s.Available {
		p := calcCompassPose(vec3(s.Accel), vec3{}, false)
		s.Pitch, s.Roll, s.BubbleX, s.BubbleY = p.Pitch, p.Roll, p.BubbleX, p.BubbleY
		b.haveHead = false
	}
	s.Calibrated = b.cal.Valid
	s.Calibration.Running = b.running
	s.Calibration.Done = b.done
	s.Calibration.Hint = b.hint
	if b.running {
		s.Calibration.Progress = calProgress(b.samples, b.count)
	} else if b.done {
		s.Calibration.Progress = 1
	}
	b.last = s
	return s
}
