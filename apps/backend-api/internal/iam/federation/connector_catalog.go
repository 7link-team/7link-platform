package federation

// socialConnectors is the compiled-in catalog, keyed by the value a tenant
// stores in its provider's issuer column. The key is part of the provider's
// security fingerprint, so changing it distrusts every identity link that was
// bound under the old one.
//
// Providers that publish OpenID Connect discovery do not belong here. Google,
// Microsoft and Entra, Apple, Slack, LinkedIn, Line, Twitch, Zoom, Okta,
// Auth0, Keycloak, Authentik, Casdoor and Logto itself are all registered as
// ProviderOIDC against their issuer URL, which gets ID-token signature
// verification and an authentication time — both strictly stronger than reading
// a profile document. Routing them through here would be a downgrade.
//
// Providers deliberately absent because their shape needs a flow this delivery
// does not implement, not because they are unsupportable: Alipay (RSA2-signed
// request envelope), WeCom and WeChat Mini Program (the applicati
// authenticates separately from the person), Steam (OpenID 2.0, not OAuth),
// Dropbox and Notion (the profile read is a POST whose body carries the
// request), Yandex and Reddit (a bespoke Authorization scheme and a mandatory
// User-Agent). Each is one profileFlow away.
var socialConnectors = map[string]connector{
	// ---- code hosting ----
	"github": {
		Authorize: "https://github.com/login/oauth/authorize",
		Token:     "https://github.com/login/oauth/access_token",
		Profile:   "https://api.github.com/user",
		Scopes:    "read:user user:email",
		Subject:   "id", Email: "email", Name: "name", Username: "login",
	},
	"gitlab": {
		Authorize: "https://gitlab.com/oauth/authorize",
		Token:     "https://gitlab.com/oauth/token",
		Profile:   "https://gitlab.com/api/v4/user",
		Scopes:    "read_user",
		Subject:   "id", Email: "email", Name: "name", Username: "username",
	},
	"gitee": {
		Authorize: "https://gitee.com/oauth/authorize",
		Token:     "https://gitee.com/oauth/token",
		Profile:   "https://gitee.com/api/v5/user",
		Scopes:    "user_info",
		Subject:   "id", Email: "email", Name: "name", Username: "login",
	},
	"bitbucket": {
		Authorize: "https://bitbucket.org/site/oauth2/authorize",
		Token:     "https://bitbucket.org/site/oauth2/access_token",
		Profile:   "https://api.bitbucket.org/2.0/user",
		Scopes:    "account",
		Subject:   "uuid", Name: "display_name", Username: "username",
	},

	// ---- consumer, international ----
	"discord": {
		Authorize: "https://discord.com/oauth2/authorize",
		Token:     "https://discord.com/api/oauth2/token",
		Profile:   "https://discord.com/api/users/@me",
		Scopes:    "identify email",
		Exchange:  formPost{secretInBody: true},
		Subject:   "id", Email: "email", EmailVerified: "verified",
		Name: "global_name", Username: "username",
	},
	"facebook": {
		Authorize: "https://www.facebook.com/v21.0/dialog/oauth",
		Token:     "https://graph.facebook.com/v21.0/oauth/access_token",
		Profile:   "https://graph.facebook.com/v21.0/me?fields=id,name,email",
		Scopes:    "public_profile email",
		Exchange:  queryGet{},
		Subject:   "id", Email: "email", Name: "name",
	},
	// X states no email address for an OAuth 2.0 app, so just-in-time
	// provisioning cannot apply; the connector links an existing account.
	"x": {
		Authorize: "https://x.com/i/oauth2/authorize",
		Token:     "https://api.x.com/2/oauth2/token",
		Profile:   "https://api.x.com/2/users/me",
		Scopes:    "users.read tweet.read",
		Subject:   "data.id", Name: "data.name", Username: "data.username",
	},
	"spotify": {
		Authorize: "https://accounts.spotify.com/authorize",
		Token:     "https://accounts.spotify.com/api/token",
		Profile:   "https://api.spotify.com/v1/me",
		Scopes:    "user-read-email user-read-private",
		Subject:   "id", Email: "email", Name: "display_name",
	},
	"kakao": {
		Authorize: "https://kauth.kakao.com/oauth/authorize",
		Token:     "https://kauth.kakao.com/oauth/token",
		Profile:   "https://kapi.kakao.com/v2/user/me",
		Scopes:    "account_email profile_nickname",
		Exchange:  formPost{secretInBody: true},
		Subject:   "id",
		Email:     "kakao_account.email", EmailVerified: "kakao_account.is_email_verified",
		Name: "kakao_account.profile.nickname",
	},
	"naver": {
		Authorize: "https://nid.naver.com/oauth2.0/authorize",
		Token:     "https://nid.naver.com/oauth2.0/token",
		Profile:   "https://openapi.naver.com/v1/nid/me",
		Scopes:    "profile",
		Subject:   "response.id", Email: "response.email", Name: "response.name",
	},

	// ---- consumer and workplace, China ----
	// WeChat's open platform states no address at all, so this connector links
	// an existing account rather than creating one.
	"wechat": {
		Authorize:         "https://open.weixin.qq.com/connect/qrconnect",
		Token:             "https://api.weixin.qq.com/sns/oauth2/access_token",
		Profile:           "https://api.weixin.qq.com/sns/userinfo",
		Scopes:            "snsapi_login",
		ClientIDParam:     "appid",
		ClientSecretParam: "secret",
		Exchange:          queryGet{},
		ProfileAuth:       tokenQuery{},
		Carry:             map[string]string{"openid": "openid"},
		Subject:           "openid", Name: "nickname",
	},
	"qq": {
		Authorize: "https://graph.qq.com/oauth2.0/authorize",
		Token:     "https://graph.qq.com/oauth2.0/token",
		Scopes:    "get_user_info",
		Subject:   "openid", Name: "nickname",
		Flow: qqFlow{},
	},
	// Weibo posts its token request and reads the secret from the body.
	"weibo": {
		Authorize:   "https://api.weibo.com/oauth2/authorize",
		Token:       "https://api.weibo.com/oauth2/access_token",
		Profile:     "https://api.weibo.com/2/users/show.json",
		Scopes:      "email",
		Exchange:    formPost{secretInBody: true},
		ProfileAuth: tokenQuery{},
		Carry:       map[string]string{"uid": "uid"},
		Subject:     "id", Name: "screen_name",
	},
	"baidu": {
		Authorize:   "https://openapi.baidu.com/oauth/2.0/authorize",
		Token:       "https://openapi.baidu.com/oauth/2.0/token",
		Profile:     "https://openapi.baidu.com/rest/2.0/passport/users/getInfo",
		Scopes:      "basic",
		Exchange:    queryGet{},
		ProfileAuth: tokenQuery{},
		Subject:     "openid", Name: "username",
	},
	"dingtalk": {
		Authorize:         "https://login.dingtalk.com/oauth2/auth",
		Token:             "https://api.dingtalk.com/v1.0/oauth2/userAccessToken",
		Profile:           "https://api.dingtalk.com/v1.0/contact/users/me",
		Scopes:            "openid",
		ClientIDParam:     "clientId",
		ClientSecretParam: "clientSecret",
		Exchange:          jsonPost{},
		TokenAccessPath:   "accessToken",
		ProfileAuth:       tokenHeader{name: "x-acs-dingtalk-access-token"},
		Subject:           "unionId", Email: "email", Name: "nick",
	},
	"feishu": {
		Authorize: "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
		Token:     "https://open.feishu.cn/open-apis/authen/v2/oauth/token",
		Profile:   "https://open.feishu.cn/open-apis/authen/v1/user_info",
		Scopes:    "contact:user.base:readonly",
		Subject:   "data.open_id", Email: "data.email", Name: "data.name",
	},
	// Lark is Feishu's international deployment: same protocol, different hosts.
	"lark": {
		Authorize: "https://accounts.larksuite.com/open-apis/authen/v1/authorize",
		Token:     "https://open.larksuite.com/open-apis/authen/v2/oauth/token",
		Profile:   "https://open.larksuite.com/open-apis/authen/v1/user_info",
		Scopes:    "contact:user.base:readonly",
		Subject:   "data.open_id", Email: "data.email", Name: "data.name",
	},
	// ---- second batch ----
	// Instagram Login replaced the Basic Display API, which was switched off in
	// December 2024: the authorization host moved to www.instagram.com and the
	// scopes were renamed, while the token endpoint stayed put. It admits only
	// professional (business or creator) accounts.
	"instagram": {
		Authorize: "https://www.instagram.com/oauth/authorize",
		Token:     "https://api.instagram.com/oauth/access_token",
		Profile:   "https://graph.instagram.com/me?fields=id,username",
		Scopes:    "instagram_business_basic",
		Exchange:  formPost{secretInBody: true},
		Subject:   "id", Username: "username",
	},
	"amazon": {
		Authorize: "https://www.amazon.com/ap/oa",
		Token:     "https://api.amazon.com/auth/o2/token",
		Profile:   "https://api.amazon.com/user/profile",
		Scopes:    "profile",
		Subject:   "user_id", Email: "email", Name: "name",
	},
	"coinbase": {
		Authorize: "https://login.coinbase.com/oauth2/auth",
		Token:     "https://login.coinbase.com/oauth2/token",
		Profile:   "https://api.coinbase.com/v2/user",
		Scopes:    "wallet:user:read identity:user:email",
		Exchange:  formPost{secretInBody: true},
		Headers:   map[string]string{"CB-VERSION": "2024-01-01"},
		Subject:   "data.id", Email: "data.email", Name: "data.name",
	},
	"figma": {
		Authorize: "https://www.figma.com/oauth",
		Token:     "https://api.figma.com/v1/oauth/token",
		Profile:   "https://api.figma.com/v1/me",
		Scopes:    "file_read",
		Subject:   "id", Email: "email", Name: "handle",
	},
	// Yandex refuses the Bearer scheme and wants its own.
	"yandex": {
		Authorize:   "https://oauth.yandex.ru/authorize",
		Token:       "https://oauth.yandex.ru/token",
		Profile:     "https://login.yandex.ru/info",
		Scopes:      "login:email login:info",
		ProfileAuth: bearerToken{scheme: "OAuth"},
		Subject:     "id", Email: "default_email", Name: "real_name", Username: "login",
	},
	// Reddit rejects any request that does not identify itself; upstreamHTTP
	// sends the deployment's own identity on every request, so nothing is
	// needed here. Its profile states whether an address is verified but never
	// sends the address, so this connector links only.
	"reddit": {
		Authorize: "https://www.reddit.com/api/v1/authorize",
		Token:     "https://www.reddit.com/api/v1/access_token",
		Profile:   "https://oauth.reddit.com/api/v1/me",
		Scopes:    "identity",
		Subject:   "id", Name: "name", Username: "name",
	},
	// Douyin nests both its token and its identifier, and wants the identifier
	// back under a different parameter name than the one it answered with.
	"douyin": {
		Authorize:       "https://open.douyin.com/platform/oauth/connect/",
		Token:           "https://open.douyin.com/oauth/access_token/",
		Profile:         "https://open.douyin.com/oauth/userinfo/",
		Scopes:          "user_info",
		ClientIDParam:   "client_key",
		Exchange:        formPost{secretInBody: true},
		TokenAccessPath: "data.access_token",
		ProfileAuth:     tokenQuery{},
		Carry:           map[string]string{"open_id": "data.open_id"},
		Subject:         "data.open_id", Name: "data.nickname",
	},
}
