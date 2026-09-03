package authn

import "github.com/mojocn/base64Captcha"

var captchaDrivers = []base64Captcha.Driver{
	base64Captcha.NewDriverDigit(64, 220, 4, 0.7, 80),
	base64Captcha.NewDriverString(64, 220, 4, 1, 4, base64Captcha.TxtAlphabet, nil, nil, nil),
	base64Captcha.NewDriverMath(64, 220, 4, 1, nil, nil, nil),
}

// Text challenges use the process-local one-shot store, matching the
// single-instance deployment contract of the source service.
var imageCaptcha = base64Captcha.NewCaptcha(captchaDrivers[0], base64Captcha.DefaultMemStore)
