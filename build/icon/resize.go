package main

import (
	"image"
	"image/color"
	"math"
)

// resize scales a square image to size x size.
//
// Down, by area averaging: each output pixel is the coverage-weighted mean of
// the source pixels under it, with colour premultiplied by alpha so the
// transparent edge of the mark does not bleed dark fringes into small icons.
//
// Up, by repeating pixels (nearest neighbour): the mark is pixel art, and a
// 128px pixel-art mark doubled to 256 or 512 should stay crisp, not blur.
func resize(src image.Image, size int) *image.NRGBA {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if size > width || size > height {
		return enlarge(src, size)
	}

	// Premultiplied RGBA, as float64, row-major.
	pixels := make([]float64, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := color.NRGBA64Model.Convert(src.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.NRGBA64)
			alpha := float64(c.A) / 0xffff
			i := (y*width + x) * 4
			pixels[i] = float64(c.R) / 0xffff * alpha
			pixels[i+1] = float64(c.G) / 0xffff * alpha
			pixels[i+2] = float64(c.B) / 0xffff * alpha
			pixels[i+3] = alpha
		}
	}

	columns := weights(width, size)
	rows := weights(height, size)

	// Horizontal pass: height x size.
	horizontal := make([]float64, height*size*4)
	for y := 0; y < height; y++ {
		for x, taps := range columns {
			out := (y*size + x) * 4
			for _, tap := range taps {
				in := (y*width + tap.index) * 4
				for k := 0; k < 4; k++ {
					horizontal[out+k] += pixels[in+k] * tap.weight
				}
			}
		}
	}

	// Vertical pass: size x size.
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y, taps := range rows {
		for x := 0; x < size; x++ {
			var sum [4]float64
			for _, tap := range taps {
				in := (tap.index*size + x) * 4
				for k := 0; k < 4; k++ {
					sum[k] += horizontal[in+k] * tap.weight
				}
			}
			alpha := sum[3]
			var c color.NRGBA
			if alpha > 0 {
				c = color.NRGBA{
					R: channel(sum[0] / alpha),
					G: channel(sum[1] / alpha),
					B: channel(sum[2] / alpha),
					A: channel(alpha),
				}
			}
			dst.SetNRGBA(x, y, c)
		}
	}

	return dst
}

// enlarge repeats source pixels: output pixel (x, y) is source pixel
// (x*width/size, y*height/size), which is exact at integer factors.
func enlarge(src image.Image, size int) *image.NRGBA {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			c := color.NRGBAModel.Convert(src.At(bounds.Min.X+x*width/size, bounds.Min.Y+y*height/size))
			dst.Set(x, y, c)
		}
	}

	return dst
}

// tap is one source pixel's share of an output pixel.
type tap struct {
	index  int
	weight float64
}

// weights maps each of `to` output positions to the source positions it
// covers along one axis of length `from`, weighted by overlap so each output
// position's weights sum to 1.
func weights(from, to int) [][]tap {
	scale := float64(from) / float64(to)
	out := make([][]tap, to)
	for i := range out {
		start, end := float64(i)*scale, float64(i+1)*scale
		for j := int(math.Floor(start)); j < int(math.Ceil(end)) && j < from; j++ {
			overlap := math.Min(end, float64(j+1)) - math.Max(start, float64(j))
			if overlap > 0 {
				out[i] = append(out[i], tap{index: j, weight: overlap / scale})
			}
		}
	}

	return out
}

func channel(v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
}
