.PHONY: dev dev-api dev-status dev-stop ci ci-fast skills-link

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

# Agent skills live once, in .agents/skills (committed, with skills-lock.json).
# Claude Code reads .claude/skills, which is a link to that directory and is
# not committed: git stores a Windows junction as a second copy of every file
# and a committed symlink checks out as a text file where core.symlinks is
# false. Run this once per clone. Node creates a junction on Windows (no
# administrator or developer mode needed; it must name an absolute target)
# and a relative symlink elsewhere.
skills-link:
	node -e "const fs=require('fs'),p=require('path'),link=p.join('.claude','skills');let s;try{s=fs.lstatSync(link)}catch{}if(s&&s.isSymbolicLink())process.exit(0);if(s){console.error(link+' exists and is not a link: move its skills into .agents/skills, remove it and run again');process.exit(1)}fs.mkdirSync('.claude',{recursive:true});fs.symlinkSync(process.platform==='win32'?p.resolve('.agents','skills'):p.join('..','.agents','skills'),link,'junction')"
