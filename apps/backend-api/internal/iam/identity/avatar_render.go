package identity

import (
	"bytes"
	"crypto/rand"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// randomAvatarPNG draws a bitmap collectible-style portrait entirely on the server.
func randomAvatarPNG(style string) ([]byte, error) {
	var seed [64]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	return avatarPNG(style, seed)
}

// avatarPNG shares the portrait renderer between random Human candidates and
// reproducible Agent candidates; callers choose the seed's source.
func avatarPNG(style string, seed [64]byte) ([]byte, error) {
	palette := []color.NRGBA{{72, 190, 163, 255}, {242, 115, 143, 255}, {110, 145, 240, 255}, {243, 197, 73, 255}, {170, 115, 220, 255}, {74, 178, 215, 255}}
	main := palette[int(seed[0])%len(palette)]
	accent := palette[(int(seed[0])+1+int(seed[1])%(len(palette)-1))%len(palette)]
	ink := color.NRGBA{35, 42, 48, 255}
	white := color.NRGBA{255, 255, 255, 255}
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	rect := func(x, y, w, h int, c color.NRGBA) {
		draw.Draw(img, image.Rect(x, y, x+w, y+h), image.NewUniform(c), image.Point{}, draw.Src)
	}
	circle := func(cx, cy, r int, c color.NRGBA) {
		for y := max(0, cy-r); y < min(256, cy+r+1); y++ {
			for x := max(0, cx-r); x < min(256, cx+r+1); x++ {
				if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r {
					img.SetNRGBA(x, y, c)
				}
			}
		}
	}
	rect(0, 0, 256, 256, color.NRGBA{235 + seed[2]%20, 235 + seed[3]%20, 235 + seed[4]%20, 255})
	rect(0, 207, 256, 49, accent)
	switch style {
	case "geometric":
		for y := 0; y < 5; y++ {
			for x := 0; x < 3; x++ {
				if seed[5+y*3+x]%2 == 0 {
					rect(43+x*34, 43+y*34, 30, 30, main)
					rect(43+(4-x)*34, 43+y*34, 30, 30, main)
				}
			}
		}
	case "pixel":
		for y := 0; y < 9; y++ {
			for x := 0; x < 5; x++ {
				if seed[5+y*5+x]%3 != 0 {
					rect(38+x*20, 38+y*20, 20, 20, main)
					rect(38+(8-x)*20, 38+y*20, 20, 20, main)
				}
			}
		}
		rect(77, 98, 28, 24, white)
		rect(151, 98, 28, 24, white)
		rect(87, 105, 12, 14, ink)
		rect(157, 105, 12, 14, ink)
		rect(108, 156, 40, 12, ink)
	case "robot":
		rect(118, 35, 20, 34, ink)
		circle(128, 33, 13, accent)
		rect(39, 107, 18, 52, accent)
		rect(199, 107, 18, 52, accent)
		rect(54, 65, 148, 131, ink)
		rect(62, 73, 132, 115, main)
		for _, x := range []int{94, 162} {
			circle(x, 117, 23, white)
			circle(x, 117, 11+int(seed[6]%5), ink)
		}
		rect(86, 158, 84, 15, ink)
		for x := 92; x < 166; x += 16 {
			rect(x, 160, 8, 11, white)
		}
	case "monster":
		circle(80, 65, 27, accent)
		circle(176, 65, 27, accent)
		circle(128, 131, 82, main)
		if seed[5]%2 == 0 {
			circle(128, 109, 31, white)
			circle(128, 111, 13, ink)
		} else {
			circle(96, 109, 24, white)
			circle(160, 109, 24, white)
			circle(98, 112, 10, ink)
			circle(158, 112, 10, ink)
		}
		circle(128, 160, 28, ink)
		rect(108, 132, 40, 16, main)
		rect(115, 146, 9, 12, white)
		rect(135, 146, 9, 12, white)
	}
	var out bytes.Buffer
	err := png.Encode(&out, img)
	return out.Bytes(), err
}
