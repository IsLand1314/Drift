package app

import (
	"fmt"
	"strings"
)

const (
	logoSceneWidth  = 52
	logoSceneHeight = 34
)

type logoColor struct{ r, g, b int }

var (
	logoVoid       = logoColor{17, 19, 27}
	logoRedGlow    = logoColor{144, 30, 48}
	logoSunEdge    = logoColor{245, 137, 36}
	logoSunCore    = logoColor{255, 220, 92}
	logoRidgeLight = logoColor{207, 126, 61}
	logoRidge      = logoColor{142, 76, 45}
	logoRidgeDark  = logoColor{77, 42, 40}
	logoSeaDark    = logoColor{25, 56, 79}
	logoSea        = logoColor{19, 106, 137}
	logoSeaLight   = logoColor{56, 193, 203}
	logoFoam       = logoColor{92, 205, 210}
)

func renderDriftLogo(workspace string) string {
	canvas := make([][]*logoColor, logoSceneHeight)
	for y := range canvas {
		canvas[y] = make([]*logoColor, logoSceneWidth)
	}
	paintLogoScene(canvas)
	var out strings.Builder
	out.WriteString("\n")
	for y := 0; y < logoSceneHeight; y += 2 {
		for x := 0; x < logoSceneWidth; x++ {
			out.WriteString(logoPixel(canvas[y][x], canvas[y+1][x]))
		}
		out.WriteByte('\n')
	}
	out.WriteString("\n                       \x1b[38;2;255;220;92mDRIFT\x1b[0m\n")
	out.WriteString("                   \x1b[38;2;207;126;61mAuthor: island\x1b[0m\n")
	out.WriteString("                \x1b[38;2;142;234;224mCoding Agent Runtime\x1b[0m\n")
	out.WriteString("                  \x1b[38;2;142;234;224m⌂ " + workspace + "\x1b[0m\n")
	return out.String()
}

func paintLogoScene(canvas [][]*logoColor) {
	paintLogoEmbers(canvas)
	paintLogoSun(canvas, 44, 5)
	paintLogoIsland(canvas)
	paintLogoSeaSpirit(canvas)
	paintLogoCurrent(canvas)
}
func logoPixel(top, bottom *logoColor) string {
	switch {
	case top == nil && bottom == nil:
		return " "
	case top != nil && bottom == nil:
		return logoFG(*top) + "▀\x1b[0m"
	case top == nil && bottom != nil:
		return logoFG(*bottom) + "▄\x1b[0m"
	default:
		return logoFG(*top) + logoBG(*bottom) + "▀\x1b[0m"
	}
}
func logoFG(c logoColor) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.r, c.g, c.b) }
func logoBG(c logoColor) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.r, c.g, c.b) }
func paintLogo(canvas [][]*logoColor, x, y int, c logoColor) {
	if x >= 0 && x < logoSceneWidth && y >= 0 && y < logoSceneHeight {
		canvas[y][x] = &c
	}
}
func paintLogoEmbers(c [][]*logoColor) {
	for _, e := range [][2]int{{37, 8}, {35, 10}, {33, 12}} {
		paintLogo(c, e[0], e[1], logoRedGlow)
		paintLogo(c, e[0]+1, e[1], logoRedGlow)
	}
}
func paintLogoSun(c [][]*logoColor, cx, cy int) {
	for y, row := range []string{"..EEE..", ".EYYYE.", "EYYYYYE", "EYYYYYE", "EYYYYYE", ".EYYYE.", "..EEE.."} {
		for x, cell := range row {
			if cell == 'E' {
				paintLogo(c, cx+x-3, cy+y-3, logoSunEdge)
			} else if cell == 'Y' {
				paintLogo(c, cx+x-3, cy+y-3, logoSunCore)
			}
		}
	}
}
func logoIslandTop(x int) int {
	switch {
	case x <= 20:
		return 16 - (x-10)*3/5
	case x <= 26:
		return 10 + (x-20)/2
	case x <= 28:
		return 13 - (x-26)/2
	case x <= 30:
		return 12 + (x-28)/2
	default:
		return 13 + (x-30)/2
	}
}
func paintLogoIsland(c [][]*logoColor) {
	for x := 10; x <= 33; x++ {
		top := logoIslandTop(x)
		for y := top; y <= 22; y++ {
			shade := logoRidge
			if y == top || (x < 23 && y-top < 2) {
				shade = logoRidgeLight
			} else if x > 24 || (x+y)%6 == 0 {
				shade = logoRidgeDark
			}
			paintLogo(c, x, y, shade)
		}
	}
}
func paintLogoSeaSpirit(c [][]*logoColor) {
	for y := 16; y <= 28; y++ {
		for x := 12; x <= 39; x++ {
			dx, dy := x-26, y-22
			if dx*dx+dy*dy*2 <= 150 {
				shade := logoSeaDark
				if y > 19 && x < 29 {
					shade = logoSea
				}
				if (x+y)%8 == 0 && y > 20 {
					shade = logoSeaLight
				}
				paintLogo(c, x, y, shade)
			}
		}
	}
	paintLogo(c, 23, 21, logoSunCore)
	paintLogo(c, 24, 21, logoSunEdge)
	paintLogo(c, 29, 21, logoSunCore)
	paintLogo(c, 30, 21, logoSunEdge)
	paintLogo(c, 24, 22, logoVoid)
	paintLogo(c, 30, 22, logoVoid)
}
func paintLogoCurrent(c [][]*logoColor) {
	for _, trail := range [][][2]int{{{14, 25}, {11, 26}, {9, 28}, {6, 29}, {3, 29}}, {{17, 27}, {14, 29}, {11, 30}, {8, 32}, {5, 32}}, {{35, 26}, {39, 27}, {42, 29}, {46, 29}, {49, 28}}, {{32, 28}, {36, 30}, {40, 31}, {44, 33}, {48, 33}}} {
		for i := 1; i < len(trail); i++ {
			paintLogoLine(c, trail[i-1], trail[i], logoSeaLight)
		}
		for i, p := range trail {
			if i%2 == 0 {
				paintLogo(c, p[0]+1, p[1], logoFoam)
			}
		}
	}
}
func paintLogoLine(c [][]*logoColor, from, to [2]int, shade logoColor) {
	dx, dy := to[0]-from[0], to[1]-from[1]
	steps := logoAbs(dx)
	if logoAbs(dy) > steps {
		steps = logoAbs(dy)
	}
	for step := 0; step <= steps; step++ {
		paintLogo(c, from[0]+dx*step/steps, from[1]+dy*step/steps, shade)
	}
}
func logoAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
