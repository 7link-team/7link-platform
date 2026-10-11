.PHONY: dev dev-api dev-status dev-stop ci ci-fast

# Keep the implementation and development settings beside the backend.
DEV_API_PORT ?= 8080
DEV_WEB_PORT ?= 8091
DEV_DEMO_ACCOUNTS ?= true
DEV_PREFILL_ACCOUNT ?= false
DEV_CAPTCHA ?= image

dev dev-api dev-status dev-stop:
	"$(MAKE)" -C apps/backend-api $@ DEV_API_PORT=$(DEV_API_PORT) DEV_WEB_PORT=$(DEV_WEB_PORT) DEV_DEMO_ACCOUNTS=$(DEV_DEMO_ACCOUNTS) DEV_PREFILL_ACCOUNT=$(DEV_PREFILL_ACCOUNT) DEV_CAPTCHA=$(DEV_CAPTCHA)

# The gates read from the repository root too, so that checking a change never
# depends on remembering which directory the backend's Makefile lives in.
ci ci-fast:
	"$(MAKE)" -C apps/backend-api $@
