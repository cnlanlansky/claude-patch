// 运行 `go run ./tools/icon` 可确定性生成 Windows 多尺寸应用图标。
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

var sizes = []int{16, 20, 24, 32, 48, 64, 128, 256}

type iconEntry struct {
	width, height, colors, reserved byte
	planes, bits                    uint16
	size, offset                    uint32
}

func main() {
	var images [][]byte
	for _, size := range sizes {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, render(size)); err != nil {
			log.Fatal(err)
		}
		images = append(images, encoded.Bytes())
	}
	output := new(bytes.Buffer)
	_ = binary.Write(output, binary.LittleEndian, uint16(0))
	_ = binary.Write(output, binary.LittleEndian, uint16(1))
	_ = binary.Write(output, binary.LittleEndian, uint16(len(images)))
	offset := uint32(6 + 16*len(images))
	for index, data := range images {
		sizeByte := byte(sizes[index])
		if sizes[index] == 256 {
			sizeByte = 0
		}
		entry := iconEntry{sizeByte, sizeByte, 0, 0, 1, 32, uint32(len(data)), offset}
		_ = binary.Write(output, binary.LittleEndian, entry)
		offset += uint32(len(data))
	}
	for _, data := range images {
		_, _ = output.Write(data)
	}
	path := filepath.Join("assets", "claude-patch.ico")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, output.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}

func render(size int) image.Image {
	scale := 4
	dimension := size * scale
	imageValue := image.NewNRGBA(image.Rect(0, 0, dimension, dimension))
	draw.Draw(imageValue, imageValue.Bounds(), image.Transparent, image.Point{}, draw.Src)

	margin := float64(dimension) * 0.055
	radius := float64(dimension) * 0.245
	for y := 0; y < dimension; y++ {
		for x := 0; x < dimension; x++ {
			coverage := roundedCoverage(float64(x)+.5, float64(y)+.5, margin, margin, float64(dimension)-margin, float64(dimension)-margin, radius)
			if coverage <= 0 {
				continue
			}
			t := float64(x+y) / float64(2*(dimension-1))
			red := blend(105, 87, t)
			green := blend(87, 71, t)
			blue := blend(217, 190, t)
			imageValue.SetNRGBA(x, y, color.NRGBA{red, green, blue, byte(255 * coverage)})
		}
	}

	white := color.NRGBA{255, 255, 255, 255}
	stroke := math.Max(float64(dimension)*0.075, 2)
	// C/P 路由弧：上下端点厚实，在 16px 托盘尺寸仍可辨认。
	drawArc(imageValue, float64(dimension)*.50, float64(dimension)*.50, float64(dimension)*.285, math.Pi*.33, math.Pi*1.67, stroke, white)
	drawLine(imageValue, float64(dimension)*.50, float64(dimension)*.215, float64(dimension)*.50, float64(dimension)*.785, stroke*.78, white)
	drawArc(imageValue, float64(dimension)*.50, float64(dimension)*.39, float64(dimension)*.17, -math.Pi*.5, math.Pi*.5, stroke*.72, white)

	bolt := [][2]float64{{.53, .24}, {.38, .54}, {.49, .54}, {.43, .78}, {.65, .45}, {.54, .45}}
	points := make([]image.Point, len(bolt))
	for index, point := range bolt {
		points[index] = image.Pt(int(point[0]*float64(dimension)), int(point[1]*float64(dimension)))
	}
	fillPolygon(imageValue, points, color.NRGBA{252, 231, 132, 255})
	return downsample(imageValue, size)
}

func roundedCoverage(x, y, left, top, right, bottom, radius float64) float64 {
	cx := math.Max(left+radius, math.Min(x, right-radius))
	cy := math.Max(top+radius, math.Min(y, bottom-radius))
	distance := math.Hypot(x-cx, y-cy)
	return math.Max(0, math.Min(1, radius-distance+.5))
}

func drawArc(target *image.NRGBA, cx, cy, radius, start, end, stroke float64, value color.NRGBA) {
	steps := int(radius * math.Abs(end-start) * 1.5)
	previousX, previousY := cx+radius*math.Cos(start), cy+radius*math.Sin(start)
	for index := 1; index <= steps; index++ {
		angle := start + (end-start)*float64(index)/float64(steps)
		x, y := cx+radius*math.Cos(angle), cy+radius*math.Sin(angle)
		drawLine(target, previousX, previousY, x, y, stroke, value)
		previousX, previousY = x, y
	}
}

func drawLine(target *image.NRGBA, x1, y1, x2, y2, width float64, value color.NRGBA) {
	minimumX := int(math.Floor(math.Min(x1, x2) - width))
	maximumX := int(math.Ceil(math.Max(x1, x2) + width))
	minimumY := int(math.Floor(math.Min(y1, y2) - width))
	maximumY := int(math.Ceil(math.Max(y1, y2) + width))
	lengthSquared := (x2-x1)*(x2-x1) + (y2-y1)*(y2-y1)
	for y := minimumY; y <= maximumY; y++ {
		for x := minimumX; x <= maximumX; x++ {
			t := ((float64(x)+.5-x1)*(x2-x1) + (float64(y)+.5-y1)*(y2-y1)) / lengthSquared
			t = math.Max(0, math.Min(1, t))
			distance := math.Hypot(float64(x)+.5-(x1+t*(x2-x1)), float64(y)+.5-(y1+t*(y2-y1)))
			if distance <= width/2 {
				blendPixel(target, x, y, value)
			}
		}
	}
}

func fillPolygon(target *image.NRGBA, points []image.Point, value color.NRGBA) {
	for y := target.Bounds().Min.Y; y < target.Bounds().Max.Y; y++ {
		for x := target.Bounds().Min.X; x < target.Bounds().Max.X; x++ {
			inside := false
			for left, right := len(points)-1, 0; right < len(points); left, right = right, right+1 {
				if (points[right].Y > y) != (points[left].Y > y) && x < (points[left].X-points[right].X)*(y-points[right].Y)/(points[left].Y-points[right].Y)+points[right].X {
					inside = !inside
				}
			}
			if inside {
				blendPixel(target, x, y, value)
			}
		}
	}
}

func blendPixel(target *image.NRGBA, x, y int, value color.NRGBA) {
	if image.Pt(x, y).In(target.Bounds()) {
		target.SetNRGBA(x, y, value)
	}
}

func downsample(source *image.NRGBA, size int) image.Image {
	target := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := source.Bounds().Dx() / size
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var red, green, blue, alpha uint32
			for sy := 0; sy < scale; sy++ {
				for sx := 0; sx < scale; sx++ {
					pixel := source.NRGBAAt(x*scale+sx, y*scale+sy)
					red += uint32(pixel.R) * uint32(pixel.A)
					green += uint32(pixel.G) * uint32(pixel.A)
					blue += uint32(pixel.B) * uint32(pixel.A)
					alpha += uint32(pixel.A)
				}
			}
			count := uint32(scale * scale)
			if alpha > 0 {
				target.SetNRGBA(x, y, color.NRGBA{byte(red / alpha), byte(green / alpha), byte(blue / alpha), byte(alpha / count)})
			}
		}
	}
	return target
}

func blend(left, right byte, amount float64) byte {
	return byte(float64(left)*(1-amount) + float64(right)*amount)
}
