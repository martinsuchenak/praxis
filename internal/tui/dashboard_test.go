package tui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGreenFade(t *testing.T) {
	cases := []struct {
		depth, total int
	}{
		{0, 10},
		{1, 10},
		{2, 10},
		{4, 10},
		{9, 10},
	}
	for _, tc := range cases {
		c := greenFade(tc.depth, tc.total)
		if c == 0 {
			t.Errorf("greenFade(%d, %d) returned zero", tc.depth, tc.total)
		}
	}
}

func TestGreenFadeDepth0IsBrightest(t *testing.T) {
	head := greenFade(0, 10)
	tail := greenFade(8, 10)
	if head <= tail {
		t.Errorf("head color %d should be brighter than tail %d", head, tail)
	}
}

func TestMaskColorInactive(t *testing.T) {
	for _, mask := range []byte{'1', '2', '3', '4'} {
		c := maskColor(mask, false)
		if c == 0 {
			t.Errorf("maskColor(%c, false) returned zero", mask)
		}
	}
}

func TestMaskColorActive(t *testing.T) {
	for _, mask := range []byte{'1', '2', '3', '4'} {
		inactive := maskColor(mask, false)
		active := maskColor(mask, true)
		if active == 0 {
			t.Errorf("maskColor(%c, true) returned zero", mask)
		}
		if active < inactive {
			t.Errorf("active color %d should be >= inactive %d for mask %c", active, inactive, mask)
		}
	}
}

func TestMaskColorActiveWhite(t *testing.T) {
	c := maskColor('4', true)
	if c != 0xFFFFFF {
		t.Errorf("maskColor('4', true) = %d, want white (0xFFFFFF)", c)
	}
}

func TestOnLine(t *testing.T) {
	cases := []struct {
		px, py, x1, y1, x2, y2 int
		want                    bool
	}{
		{5, 5, 0, 0, 10, 10, true},
		{0, 0, 0, 0, 10, 10, true},
		{10, 10, 0, 0, 10, 10, true},
		{3, 3, 0, 0, 10, 10, true},
		{5, 3, 0, 0, 10, 10, false},
		{11, 11, 0, 0, 10, 10, false},
		{3, 0, 0, 0, 10, 0, true},
		{5, 5, 5, 5, 5, 5, false},
	}
	for _, tc := range cases {
		got := onLine(tc.px, tc.py, tc.x1, tc.y1, tc.x2, tc.y2)
		if got != tc.want {
			t.Errorf("onLine(%d,%d, %d,%d->%d,%d) = %v, want %v",
				tc.px, tc.py, tc.x1, tc.y1, tc.x2, tc.y2, got, tc.want)
		}
	}
}

func TestAvatarGlyph(t *testing.T) {
	g := avatarGlyph("testbot", 0, 0, 0)
	if g == 0 {
		t.Error("avatarGlyph returned zero rune")
	}
}

func TestAvatarGlyphDeterministic(t *testing.T) {
	g1 := avatarGlyph("bot", 3, 4, 10)
	g2 := avatarGlyph("bot", 3, 4, 10)
	if g1 != g2 {
		t.Errorf("avatarGlyph not deterministic: %c vs %c", g1, g2)
	}
}

func TestAvatarGlyphDiffersByName(t *testing.T) {
	g1 := avatarGlyph("alpha", 0, 0, 0)
	g2 := avatarGlyph("beta", 0, 0, 0)
	if g1 == g2 {
		t.Logf("warning: different names produced same glyph (possible collision)")
	}
}

func TestAvatarGlyphChangesOverFrames(t *testing.T) {
	g0 := avatarGlyph("bot", 0, 0, 0)
	changed := false
	for frame := 1; frame < 30; frame++ {
		if avatarGlyph("bot", 0, 0, frame) != g0 {
			changed = true
			break
		}
	}
	if !changed {
		t.Error("avatarGlyph should change over frames for organic flicker")
	}
}

func TestRandomGlyph(t *testing.T) {
	for i := 0; i < 100; i++ {
		g := randomGlyph()
		if g == 0 {
			t.Fatal("randomGlyph returned zero rune")
		}
		found := false
		for _, mg := range matrixGlyphs {
			if mg == g {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("randomGlyph returned %c not in matrixGlyphs", g)
		}
	}
}

func TestFaceSpecRenderDimensions(t *testing.T) {
	for i, spec := range faceSpecs {
		lines := spec.render()
		h := len(spec.halfWidths)
		if len(lines) != h {
			t.Errorf("faceSpec[%d]: got %d lines, want %d", i, len(lines), h)
		}
		for j, line := range lines {
			if len(line) != maskW {
				t.Errorf("faceSpec[%d] line %d: got width %d, want %d", i, j, len(line), maskW)
			}
		}
	}
}

func TestFaceSpecRenderNonEmpty(t *testing.T) {
	for i, spec := range faceSpecs {
		lines := spec.render()
		allZero := true
		for _, line := range lines {
			if strings.ContainsAny(line, "1234") {
				allZero = false
				break
			}
		}
		if allZero {
			t.Errorf("faceSpec[%d]: rendered all zeros", i)
		}
	}
}

func TestFaceSpecRenderContainsFill(t *testing.T) {
	for i, spec := range faceSpecs {
		lines := spec.render()
		hasFill := false
		for _, line := range lines {
			if strings.ContainsRune(line, '2') || strings.ContainsRune(line, '3') {
				hasFill = true
				break
			}
		}
		if !hasFill {
			t.Errorf("faceSpec[%d]: no fill characters ('2' or '3') in rendered mask", i)
		}
	}
}

func TestFaceSpecRenderEyesWithEyeRX(t *testing.T) {
	for i, spec := range faceSpecs {
		if spec.eyeRX <= 0 {
			continue
		}
		lines := spec.render()
		hasEye := false
		for _, line := range lines {
			if strings.ContainsRune(line, '3') || strings.ContainsRune(line, '4') {
				hasEye = true
				break
			}
		}
		if !hasEye {
			t.Errorf("faceSpec[%d] has eyeRX=%v but no '3' or '4' in mask", i, spec.eyeRX)
		}
	}
}

func TestFaceSpecRenderMaskValues(t *testing.T) {
	for i, spec := range faceSpecs {
		lines := spec.render()
		for j, line := range lines {
			for k, c := range line {
				if c != '0' && c != '1' && c != '2' && c != '3' && c != '4' {
					t.Errorf("faceSpec[%d] [%d][%d]: invalid mask value %c", i, j, k, c)
				}
			}
		}
	}
}

func TestFaceSpecRenderSoftEdges(t *testing.T) {
	spec := faceSpecs[0]
	lines := spec.render()
	hasGlow := false
	for _, line := range lines {
		if strings.ContainsRune(line, '1') {
			hasGlow = true
			break
		}
	}
	if !hasGlow {
		t.Error("face should have '1' glow/edge cells for soft edges")
	}

	fillHasGlowNeighbor := false
	for row := 0; row < len(lines); row++ {
		for col := 0; col < len(lines[row]); col++ {
			if lines[row][col] != '2' && lines[row][col] != '3' {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					nr, nc := row+dy, col+dx
					if nr >= 0 && nr < len(lines) && nc >= 0 && nc < len(lines[nr]) {
						if lines[nr][nc] == '1' {
							fillHasGlowNeighbor = true
						}
					}
				}
			}
		}
	}
	if !fillHasGlowNeighbor {
		t.Error("at least some fill cells should have '1' glow cells within 2 cells")
	}
}

func TestFaceSpecRenderCenterIsFilled(t *testing.T) {
	spec := faceSpecs[0]
	lines := spec.render()
	cx := maskW / 2
	cy := len(spec.halfWidths) / 2
	mask := lines[cy][cx]
	if mask == '0' || mask == '1' {
		t.Errorf("center cell should be fill ('2' or '3'), got '%c'", mask)
	}
}

func TestFaceSpecRenderDistanceField(t *testing.T) {
	spec := faceSpecs[0]
	lines := spec.render()
	transitions := 0
	cx := maskW / 2
	for row := 1; row < len(lines); row++ {
		if lines[row][cx] != lines[row-1][cx] {
			transitions++
		}
	}
	if transitions == 0 {
		t.Error("expected brightness transitions across rows (distance field should vary)")
	}
}

func TestFaceSpecVisor(t *testing.T) {
	for _, spec := range faceSpecs {
		if spec.extras != "visor" {
			continue
		}
		lines := spec.render()
		for y := spec.eyeY; y <= spec.eyeY+int(math.Ceil(spec.eyeRY)); y++ {
			if y < 0 || y >= len(lines) {
				continue
			}
			for x := 0; x < len(lines[y]); x++ {
				if lines[y][x] != '0' && lines[y][x] != '4' {
					t.Errorf("visor row %d col %d: got '%c', want '4' or '0'", y, x, lines[y][x])
				}
			}
		}
	}
}

func TestFaceSpecTeeth(t *testing.T) {
	for _, spec := range faceSpecs {
		if spec.extras != "teeth" {
			continue
		}
		lines := spec.render()
		if spec.mouthY < 0 || spec.mouthY >= len(lines) {
			continue
		}
		row := lines[spec.mouthY]
		hasTooth := false
		for x, c := range row {
			if c == '4' && x%2 == 0 {
				hasTooth = true
				break
			}
		}
		if !hasTooth {
			t.Error("teeth face should have alternating '4' highlights on mouth row")
		}
	}
}

func TestFaceSpecAntenna(t *testing.T) {
	for _, spec := range faceSpecs {
		if spec.extras != "antenna" {
			continue
		}
		lines := spec.render()
		mid := maskW / 2
		if len(lines) == 0 || len(lines[0]) <= mid {
			t.Fatal("mask too small")
		}
		if lines[0][mid] != '4' {
			t.Errorf("antenna tip at [0][%d] = '%c', want '4'", mid, lines[0][mid])
		}
	}
}

func TestLoadAvatar(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing_file", func(t *testing.T) {
		result := loadAvatar(dir)
		if result != nil {
			t.Error("expected nil for missing file")
		}
	})

	t.Run("too_few_lines", func(t *testing.T) {
		path := filepath.Join(dir, "avatar.txt")
		if err := os.WriteFile(path, []byte("000000000000000\n000000000000000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := loadAvatar(dir)
		if result != nil {
			t.Error("expected nil for < 5 lines")
		}
	})

	t.Run("valid_avatar", func(t *testing.T) {
		lines := make([]string, 8)
		for i := range lines {
			lines[i] = strings.Repeat("1", maskW)
		}
		path := filepath.Join(dir, "avatar.txt")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := loadAvatar(dir)
		if len(result) != 8 {
			t.Fatalf("expected 8 lines, got %d", len(result))
		}
		for i, line := range result {
			if len(line) != maskW {
				t.Errorf("line %d: width %d, want %d", i, len(line), maskW)
			}
		}
	})

	t.Run("trims_whitespace", func(t *testing.T) {
		lines := make([]string, 8)
		for i := range lines {
			lines[i] = strings.Repeat("2", maskW) + "  "
		}
		path := filepath.Join(dir, "avatar.txt")
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		result := loadAvatar(dir)
		for i, line := range result {
			if len(line) != maskW {
				t.Errorf("line %d: not trimmed, width %d, want %d", i, len(line), maskW)
			}
		}
	})
}

func TestLoadAvatarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	spec := faceSpecs[0]
	lines := spec.render()
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "avatar.txt"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded := loadAvatar(dir)
	if len(loaded) != len(lines) {
		t.Fatalf("loaded %d lines, wrote %d", len(loaded), len(lines))
	}
	for i, line := range loaded {
		if line != lines[i] {
			t.Errorf("line %d mismatch: got %q, want %q", i, line, lines[i])
		}
	}
}

func TestBrightnessColor(t *testing.T) {
	for _, mask := range []byte{'1', '2', '3', '4'} {
		c := brightnessColor(mask)
		if c == 0 {
			t.Errorf("brightnessColor(%c) returned zero", mask)
		}
	}
}

func TestBrightnessColorGradient(t *testing.T) {
	c1 := brightnessColor('1')
	c2 := brightnessColor('2')
	c3 := brightnessColor('3')
	c4 := brightnessColor('4')
	if c1 >= c2 || c2 >= c3 || c3 >= c4 {
		t.Errorf("brightness not monotonically increasing: %d < %d < %d < %d", c1, c2, c3, c4)
	}
}

func TestMinMax(t *testing.T) {
	if max(1, 2) != 2 {
		t.Error("max(1,2) != 2")
	}
	if max(3, 1) != 3 {
		t.Error("max(3,1) != 3")
	}
	if min(1, 2) != 1 {
		t.Error("min(1,2) != 1")
	}
	if min(3, 1) != 1 {
		t.Error("min(3,1) != 1")
	}
}

func TestAbs(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0}, {1, 1}, {-1, 1}, {42, 42}, {-42, 42},
	}
	for _, tc := range cases {
		if got := abs(tc.in); got != tc.want {
			t.Errorf("abs(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
