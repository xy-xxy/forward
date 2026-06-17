// genicon - 生成 forward.ico
//
// 设计:深蓝圆角方底 + 两个白色节点 + 一个白色"转发"箭头。
// 输出多分辨率(16/32/48/64/128/256)的 PNG 打包成 ICO(Vista+ 支持 PNG 嵌入)。
//
// 用法:
//   go run ./tools/genicon -o forward.ico

package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	out := flag.String("o", "forward.ico", "输出 .ico 路径")
	flag.Parse()

	sizes := []int{16, 32, 48, 64, 128, 256}
	pngs := make([][]byte, 0, len(sizes))
	for _, s := range sizes {
		img := renderLogo(s)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		pngs = append(pngs, buf.Bytes())
	}
	ico := buildICO(sizes, pngs)
	if err := os.WriteFile(*out, ico, 0644); err != nil {
		panic(err)
	}
}

// renderLogo 在 size×size 画 logo。
func renderLogo(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 配色
	bg := color.RGBA{0x1f, 0x6f, 0xeb, 0xff} // 深蓝
	fg := color.RGBA{0xff, 0xff, 0xff, 0xff} // 白
	tr := color.RGBA{0x00, 0x00, 0x00, 0x00} // 透明

	// 1. 填透明背景
	fillRect(img, 0, 0, size, size, tr)

	// 2. 圆角方底
	radius := float64(size) * 0.18
	fillRoundedRect(img, 0, 0, size, size, radius, bg)

	// 3. 两个节点 + 箭头
	// 节点圆心:左 (cx1,cy)、右 (cx2,cy);箭头从 cx1+r1 到 cx2-r2 头部
	cy := float64(size) / 2
	cx1 := float64(size) * 0.26
	cx2 := float64(size) * 0.74
	r := math.Max(2, float64(size)*0.11)
	fillCircle(img, cx1, cy, r, fg)
	fillCircle(img, cx2, cy, r, fg)

	// 箭头线段
	lineW := math.Max(1, float64(size)*0.07)
	x0 := cx1 + r + lineW*0.4
	x1 := cx2 - r - lineW*0.4
	fillRect(img,
		int(math.Round(x0)), int(math.Round(cy-lineW/2)),
		int(math.Round(x1)), int(math.Round(cy+lineW/2)),
		fg)

	// 箭头头(三角形,顶点指向右节点)
	tipX := x1 + lineW*0.2
	hh := math.Max(2, float64(size)*0.10) // 半高
	hw := math.Max(2, float64(size)*0.10) // 长度
	fillTriangle(img,
		tipX, cy,
		tipX-hw, cy-hh,
		tipX-hw, cy+hh,
		fg)

	return img
}

// ---------- 几何绘制 ----------

func setPixel(img *image.RGBA, x, y int, c color.RGBA) {
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		return
	}
	img.SetRGBA(x, y, c)
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			setPixel(img, x, y, c)
		}
	}
}

func fillCircle(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	r2 := r * r
	x0 := int(math.Floor(cx - r))
	x1 := int(math.Ceil(cx + r))
	y0 := int(math.Floor(cy - r))
	y1 := int(math.Ceil(cy + r))
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= r2 {
				setPixel(img, x, y, c)
			}
		}
	}
}

func fillRoundedRect(img *image.RGBA, x0, y0, x1, y1 int, r float64, c color.RGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			fx := float64(x) + 0.5
			fy := float64(y) + 0.5
			// 四角:判断到圆心的距离
			var cx, cy float64
			inCorner := false
			switch {
			case fx < float64(x0)+r && fy < float64(y0)+r:
				cx, cy = float64(x0)+r, float64(y0)+r
				inCorner = true
			case fx > float64(x1)-r && fy < float64(y0)+r:
				cx, cy = float64(x1)-r, float64(y0)+r
				inCorner = true
			case fx < float64(x0)+r && fy > float64(y1)-r:
				cx, cy = float64(x0)+r, float64(y1)-r
				inCorner = true
			case fx > float64(x1)-r && fy > float64(y1)-r:
				cx, cy = float64(x1)-r, float64(y1)-r
				inCorner = true
			}
			if inCorner {
				dx := fx - cx
				dy := fy - cy
				if dx*dx+dy*dy > r*r {
					continue
				}
			}
			setPixel(img, x, y, c)
		}
	}
}

func fillTriangle(img *image.RGBA, x0, y0, x1, y1, x2, y2 float64, c color.RGBA) {
	minX := int(math.Floor(math.Min(x0, math.Min(x1, x2))))
	maxX := int(math.Ceil(math.Max(x0, math.Max(x1, x2))))
	minY := int(math.Floor(math.Min(y0, math.Min(y1, y2))))
	maxY := int(math.Ceil(math.Max(y0, math.Max(y1, y2))))
	sign := func(px, py, ax, ay, bx, by float64) float64 {
		return (px-bx)*(ay-by) - (ax-bx)*(py-by)
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			fx := float64(x) + 0.5
			fy := float64(y) + 0.5
			d1 := sign(fx, fy, x0, y0, x1, y1)
			d2 := sign(fx, fy, x1, y1, x2, y2)
			d3 := sign(fx, fy, x2, y2, x0, y0)
			hasNeg := d1 < 0 || d2 < 0 || d3 < 0
			hasPos := d1 > 0 || d2 > 0 || d3 > 0
			if !(hasNeg && hasPos) {
				setPixel(img, x, y, c)
			}
		}
	}
}

// ---------- ICO 封装 ----------

func buildICO(sizes []int, pngs [][]byte) []byte {
	var buf bytes.Buffer
	// ICONDIR (6 bytes): reserved=0, type=1, count
	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(len(sizes)))

	// ICONDIRENTRY (16 bytes each)
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		w := byte(s)
		h := byte(s)
		if s >= 256 {
			w = 0 // 0 表示 256
			h = 0
		}
		buf.WriteByte(w)
		buf.WriteByte(h)
		buf.WriteByte(0)                                    // 调色板
		buf.WriteByte(0)                                    // 保留
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bit count
		binary.Write(&buf, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
