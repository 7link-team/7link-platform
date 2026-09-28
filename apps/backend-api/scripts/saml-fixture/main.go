// Command saml-fixture serves a loopback-only signed upstream for browser tests.
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"flag"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
)

type configuration struct {
	Subject  string `json:"subject"`
	Email    string `json:"email"`
	EntityID string `json:"entity_id"`
	Callback string `json:"callback"`
}

type fixture struct {
	sync.RWMutex
	config configuration
}

func (f *fixture) GetServiceProvider(_ *http.Request, entityID string) (*saml.EntityDescriptor, error) {
	f.RLock()
	defer f.RUnlock()
	if entityID != f.config.EntityID {
		return nil, os.ErrNotExist
	}
	acs, err := url.Parse(f.config.Callback)
	if err != nil {
		return nil, err
	}
	sp := &saml.ServiceProvider{EntityID: entityID, AcsURL: *acs}
	return sp.Metadata(), nil
}

func (f *fixture) GetSession(w http.ResponseWriter, _ *http.Request, _ *saml.IdpAuthnRequest) *saml.Session {
	f.RLock()
	defer f.RUnlock()
	if f.config.Subject == "" {
		http.Error(w, "Configure fixture first", http.StatusConflict)
		return nil
	}
	now := time.Now().UTC()
	return &saml.Session{ID: f.config.Subject, Index: f.config.Subject,
		NameID: f.config.Subject, NameIDFormat: string(saml.PersistentNameIDFormat),
		UserName: f.config.Subject, UserEmail: f.config.Email, UserCommonName: f.config.Subject,
		CreateTime: now, ExpireTime: now.Add(5 * time.Minute),
		CustomAttributes: []saml.Attribute{{Name: "email", Values: []saml.AttributeValue{{Type: "xs:string", Value: f.config.Email}}},
			{Name: "display_name", Values: []saml.AttributeValue{{Type: "xs:string", Value: f.config.Subject}}}}}
}

func main() {
	port := flag.String("port", "0", "loopback port")
	flag.Parse()
	listener, err := net.Listen("tcp", "127.0.0.1:"+*port)
	if err != nil {
		log.Fatal(err)
	}
	base := "http://" + listener.Addr().String()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Loopback SAML fixture"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		log.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		log.Fatal(err)
	}
	metadata, _ := url.Parse(base + "/metadata")
	sso, _ := url.Parse(base + "/sso")
	f := &fixture{}
	idp := &saml.IdentityProvider{Key: key, Certificate: cert, MetadataURL: *metadata, SSOURL: *sso,
		Logger: log.Default(), ServiceProviderProvider: f, SessionProvider: f,
		SignatureMethod: dsig.RSASHA256SignatureMethod}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metadata", idp.ServeMetadata)
	mux.HandleFunc("GET /sso", idp.ServeSSO)
	mux.HandleFunc("POST /configure", func(w http.ResponseWriter, r *http.Request) {
		var input configuration
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			http.Error(w, "Invalid fixture configuration", http.StatusBadRequest)
			return
		}
		callback, err := url.Parse(input.Callback)
		if err != nil || callback.Hostname() != "127.0.0.1" || input.Subject == "" || input.EntityID == "" {
			http.Error(w, "Only a configured loopback callback is allowed", http.StatusBadRequest)
			return
		}
		f.Lock()
		f.config = input
		f.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	log.Printf("SAML fixture %s", base)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.Serve(listener))
}
