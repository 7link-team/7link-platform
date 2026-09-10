package authn

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mojocn/base64Captcha"
	"github.com/wenlng/go-captcha/v2/base/option"
	"github.com/wenlng/go-captcha/v2/click"
	"github.com/wenlng/go-captcha/v2/slide"
)

type captchaKind string

const (
	captchaText  captchaKind = "text"
	captchaSlide captchaKind = "slide"
	captchaClick captchaKind = "click"
)

const (
	slideW, slideH = 300, 220
	slidePadding   = 8
	clickPadding   = 20
	interactiveTTL = 5 * time.Minute
)

var (
	slideBackgrounds []image.Image
	clickBackgrounds []image.Image
	slideGraph       *slide.GraphImage
)

func secureIntn(bound int) int {
	if bound <= 0 {
		panic("captcha random bound must be positive")
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(bound)))
	if err != nil {
		panic(fmt.Errorf("read captcha entropy: %w", err))
	}
	return int(value.Int64())
}

func secureByte(bound byte) byte {
	if bound == 0 {
		panic("captcha random byte bound must be positive")
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(bound)))
	if err != nil {
		panic(fmt.Errorf("read captcha entropy: %w", err))
	}
	return byte(value.Uint64())
}

func secureByteOffset(base, bound byte) byte {
	if bound == 0 || base > 255-(bound-1) {
		panic("captcha random byte range is invalid")
	}
	return base + secureByte(bound)
}

func init() {
	for range 3 {
		background := genCaptchaBackground()
		slideBackgrounds = append(slideBackgrounds, background)
		clickBackgrounds = append(clickBackgrounds, background)
	}
	slideGraph = genSlideGraph()
}

var interactiveAnswers = struct {
	sync.Mutex
	values    map[string]string
	expiresAt map[string]time.Time
}{values: map[string]string{}, expiresAt: map[string]time.Time{}}

func storeInteractiveAnswer(id, value string) {
	now := time.Now()
	interactiveAnswers.Lock()
	defer interactiveAnswers.Unlock()
	for storedID, expiresAt := range interactiveAnswers.expiresAt {
		if !expiresAt.After(now) {
			delete(interactiveAnswers.values, storedID)
			delete(interactiveAnswers.expiresAt, storedID)
		}
	}
	interactiveAnswers.values[id] = value
	interactiveAnswers.expiresAt[id] = now.Add(interactiveTTL)
}

func takeInteractiveAnswer(id string) string {
	now := time.Now()
	interactiveAnswers.Lock()
	defer interactiveAnswers.Unlock()
	value, ok := interactiveAnswers.values[id]
	expiresAt := interactiveAnswers.expiresAt[id]
	delete(interactiveAnswers.values, id)
	delete(interactiveAnswers.expiresAt, id)
	if !ok || !expiresAt.After(now) {
		return ""
	}
	return value
}

func genCaptchaBackground() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, slideW, slideH))
	top := color.RGBA{secureByteOffset(90, 110), secureByteOffset(70, 90), secureByteOffset(120, 100), 255}
	bottom := color.RGBA{secureByteOffset(25, 50), secureByteOffset(35, 55), secureByteOffset(50, 70), 255}
	for y := 0; y < slideH; y++ {
		t := float64(y) / float64(slideH)
		lineColor := color.RGBA{
			R: uint8(float64(top.R) + (float64(bottom.R)-float64(top.R))*t),
			G: uint8(float64(top.G) + (float64(bottom.G)-float64(top.G))*t),
			B: uint8(float64(top.B) + (float64(bottom.B)-float64(top.B))*t),
			A: 255,
		}
		draw.Draw(img, image.Rect(0, y, slideW, y+1), &image.Uniform{C: lineColor}, image.Point{}, draw.Src)
	}
	for range 24 {
		centerX, centerY, radius := secureIntn(slideW), secureIntn(slideH), 6+secureIntn(22)
		circleColor := color.RGBA{secureByte(255), secureByte(255), secureByte(255), secureByteOffset(50, 90)}
		for deltaY := -radius; deltaY <= radius; deltaY++ {
			for deltaX := -radius; deltaX <= radius; deltaX++ {
				if deltaX*deltaX+deltaY*deltaY > radius*radius {
					continue
				}
				x, y := centerX+deltaX, centerY+deltaY
				if x >= 0 && x < slideW && y >= 0 && y < slideH {
					img.SetRGBA(x, y, circleColor)
				}
			}
		}
	}
	return img
}

func genSlideGraph() *slide.GraphImage {
	const size = 50
	mask := image.NewRGBA(image.Rect(0, 0, size, size))
	overlay := image.NewRGBA(image.Rect(0, 0, size, size))
	shadow := image.NewRGBA(image.Rect(0, 0, size, size))
	tint := color.RGBA{secureByteOffset(120, 100), secureByteOffset(100, 110), secureByteOffset(130, 110), 255}
	shape := func(x, y int) bool {
		if x >= 3 && x < size-5 && y >= 3 && y < size-3 {
			return true
		}
		return x >= size-5 && x < size-1 && y >= size/2-7 && y < size/2+7
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !shape(x, y) {
				continue
			}
			mask.Set(x, y, color.White)
			overlay.Set(x, y, tint)
			shadow.Set(x, y, color.RGBA{A: 150})
		}
	}
	return &slide.GraphImage{OverlayImage: overlay, ShadowImage: shadow, MaskImage: mask}
}

type captchaOutput struct {
	Type        string `json:"type"`
	CaptchaID   string `json:"captcha_id"`
	ImageBase64 string `json:"image_base64,omitempty"`
	MasterImage string `json:"master_image,omitempty"`
	TileImage   string `json:"tile_image,omitempty"`
	ThumbImage  string `json:"thumb_image,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

// issueCaptchaOf builds one challenge of the requested kind. The kind is chosen
// by the deployment's offering rather than here, so which challenges exist and
// which a deployment presents stay separate questions.
func issueCaptchaOf(kind captchaKind) (*captchaOutput, error) {
	return issueCaptchaWithStore(kind, nil)
}

func issueCaptchaWithStore(kind captchaKind, store base64Captcha.Store) (*captchaOutput, error) {
	put := func(id, answer string) error {
		if store != nil {
			return store.Set(id, answer)
		}
		storeInteractiveAnswer(id, answer)
		return nil
	}
	switch kind {
	case captchaText:
		generator := imageCaptcha
		if store != nil {
			generator = base64Captcha.NewCaptcha(captchaDrivers[0], store)
		}
		id, encoded, _, err := generator.Generate()
		if err != nil {
			return nil, err
		}
		return &captchaOutput{Type: string(captchaText), CaptchaID: id, ImageBase64: encoded}, nil
	case captchaSlide:
		builder := slide.NewBuilder(slide.WithImageSize(option.Size{Width: slideW, Height: slideH}))
		builder.SetResources(
			slide.WithGraphImages([]*slide.GraphImage{slideGraph}),
			slide.WithBackgrounds(slideBackgrounds),
		)
		data, err := builder.Make().Generate()
		if err != nil {
			return nil, err
		}
		block := data.GetData()
		id, err := randomToken(16)
		if err != nil {
			return nil, err
		}
		if err := put(id, fmt.Sprintf("S:%d:%d", block.DX, block.DY)); err != nil {
			return nil, err
		}
		master, err := data.GetMasterImage().ToBase64()
		if err != nil {
			return nil, err
		}
		tile, err := data.GetTileImage().ToBase64()
		if err != nil {
			return nil, err
		}
		return &captchaOutput{
			Type: string(captchaSlide), CaptchaID: id,
			MasterImage: master, TileImage: tile,
			Width: slideW, Height: slideH,
		}, nil
	case captchaClick:
		builder := click.NewBuilder()
		builder.SetResources(
			click.WithBackgrounds(clickBackgrounds),
			click.WithChars([]string{
				"0", "1", "2", "3", "4", "5", "6", "7", "8", "9",
				"a", "b", "c", "d", "e", "f", "g", "h", "j", "k", "m", "n",
				"p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z",
			}),
			click.WithFonts(base64Captcha.DefaultEmbeddedFonts.LoadFontsByNames([]string{
				"fonts/3Dumb.ttf", "fonts/Comismsh.ttf", "fonts/ApothecaryFont.ttf",
			})),
		)
		data, err := builder.Make().Generate()
		if err != nil {
			return nil, err
		}
		dotsJSON, err := json.Marshal(data.GetData())
		if err != nil {
			return nil, err
		}
		id, err := randomToken(16)
		if err != nil {
			return nil, err
		}
		if err := put(id, "C:"+string(dotsJSON)); err != nil {
			return nil, err
		}
		master, err := data.GetMasterImage().ToBase64()
		if err != nil {
			return nil, err
		}
		thumb, err := data.GetThumbImage().ToBase64()
		if err != nil {
			return nil, err
		}
		return &captchaOutput{
			Type: string(captchaClick), CaptchaID: id,
			MasterImage: master, ThumbImage: thumb,
			Width: slideW, Height: slideH,
		}, nil
	default:
		// Unreachable through the offering, which admits nothing else. Kept as
		// an error rather than a panic so adding a kind and forgetting to build
		// it fails one request instead of the process.
		return nil, fmt.Errorf("unknown captcha kind %q", string(kind))
	}
}

// verifyCaptchaOf validates and consumes one challenge. Whether the deployment
// offers this kind at all is decided by the offering before this is reached.
func verifyCaptchaOf(kind captchaKind, id, answer string) bool {
	return verifyCaptchaWithStore(kind, id, answer, nil)
}

func verifyCaptchaWithStore(kind captchaKind, id, answer string, store base64Captcha.Store) bool {
	if id == "" || answer == "" {
		return false
	}
	take := takeInteractiveAnswer
	if store != nil {
		take = func(id string) string { return store.Get(id, true) }
	}
	switch kind {
	case captchaText:
		if store != nil {
			return store.Verify(id, answer, true)
		}
		return imageCaptcha.Verify(id, answer, true)
	case captchaSlide:
		stored := take(id)
		var tileX, tileY, userX int
		if _, err := fmt.Sscanf(stored, "S:%d:%d", &tileX, &tileY); err != nil {
			return false
		}
		if _, err := fmt.Sscanf(answer, "%d", &userX); err != nil {
			return false
		}
		return slide.Validate(tileX, tileY, userX, tileY, slidePadding)
	case captchaClick:
		stored := take(id)
		if !strings.HasPrefix(stored, "C:") {
			return false
		}
		var dots map[int]*click.Dot
		if err := json.Unmarshal([]byte(strings.TrimPrefix(stored, "C:")), &dots); err != nil {
			return false
		}
		used := make(map[int]bool, len(dots))
		for _, part := range strings.Split(answer, ";") {
			var userX, userY int
			if _, err := fmt.Sscanf(part, "%d,%d", &userX, &userY); err != nil {
				return false
			}
			matched := false
			for index, dot := range dots {
				if used[index] {
					continue
				}
				if click.Validate(userX, userY, dot.X, dot.Y, slideW, slideH, clickPadding) {
					used[index] = true
					matched = true
					break
				}
			}
			if !matched {
				return false
			}
		}
		return len(used) == len(dots)
	default:
		return false
	}
}

func randomToken(size int) (string, error) {
	if size <= 0 {
		return "", fmt.Errorf("token size must be positive")
	}
	data := make([]byte, size)
	if _, err := cryptorand.Read(data); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
