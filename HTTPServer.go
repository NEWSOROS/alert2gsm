package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/valyala/fasthttp"
	"github.com/vardius/gorouter/v4"
)

type HTTPServerConfiguration struct {
	ExternalEndpoint string `yaml:"external_endpoint"`
	HTTP             struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen"`
	} `yaml:"http"`
	HTTPS struct {
		Enabled           bool   `yaml:"enabled"`
		Listen            string `yaml:"listen"`
		SslCertificate    string `yaml:"ssl_certificate"`
		SslCertificateKey string `yaml:"ssl_certificate_key"`
	} `yaml:"https"`
	Webhooks struct {
		Call struct {
			Enabled bool `yaml:"enabled"`
		} `yaml:"call"`
		Grafana struct {
			Enabled    bool   `yaml:"enabled"`
			Username   string `yaml:"username"`
			Password   string `yaml:"password"`
			PreMessage string `yaml:"pre_message"`
		} `yaml:"grafana"`
		Twilio struct {
			Enabled bool     `yaml:"enabled"`
			Voice   string   `yaml:"voice"`
			Notify  []string `yaml:"notify"`
			From    string   `yaml:"from"`
		} `yaml:"twilio"`
	} `yaml:"webhooks"`
}

type HTTPServer struct {
	router           gorouter.FastHTTPRouter
	requiredUser     []byte
	requiredPassword []byte
	Configuration    HTTPServerConfiguration
	Twilio           Twilio
	logger           string
	storageMu        sync.RWMutex
}

func (server *HTTPServer) Start() {
	log.Debug("HTTPServer.Start()")
	server.requiredUser = []byte(server.Configuration.Webhooks.Grafana.Username)
	server.requiredPassword = []byte(server.Configuration.Webhooks.Grafana.Password)

	server.router = gorouter.NewFastHTTPRouter()
	if server.Configuration.Webhooks.Call.Enabled {
		server.router.GET("/call/", server.handlerCall)
	}

	if server.Configuration.Webhooks.Twilio.Enabled {
		server.router.POST("/webhook/twilio/{id}.xml", server.handlerWebhookTwilio)
	}
	if server.Configuration.Webhooks.Grafana.Enabled {
		server.router.POST("/webhook/grafana/", server.handlerWebhookGrafana)
		server.router.USE("POST", "/webhook/grafana/", server.BasicAuth)
	}

	if server.Configuration.HTTP.Enabled {
		log.WithFields(log.Fields{
			"http": server.Configuration.HTTP,
		}).Info("Starting http server")

		go func() {
			err := fasthttp.ListenAndServe(server.Configuration.HTTP.Listen, server.router.HandleFastHTTP)
			if err != nil {
				log.Fatal(err)
			}
		}()
	}

	if server.Configuration.HTTPS.Enabled {
		go func() {
			var (
				err error
			)

			if server.Configuration.HTTPS.SslCertificate == "" || server.Configuration.HTTPS.SslCertificateKey == "" {
				tls_server := &fasthttp.Server{
					Handler: server.router.HandleFastHTTP,
				}
				cert, priv, err := fasthttp.GenerateTestCertificate(server.Configuration.HTTPS.Listen)
				if err != nil {
					log.Fatal(err)
				}
				err = tls_server.AppendCertEmbed(cert, priv)
				if err != nil {
					log.Fatal(err)
				}
				log.WithFields(log.Fields{
					"https": server.Configuration.HTTPS,
				}).Warn("Starting https server with auto-generated certificate - ssl_certificate or ssl_certificate_key are empty")

				err = tls_server.ListenAndServeTLS(server.Configuration.HTTPS.Listen, "", "")
				if err != nil {
					log.Fatal(err)
				}

			} else {
				log.WithFields(log.Fields{
					"https": server.Configuration.HTTPS,
				}).Info("Starting https server")

				err = fasthttp.ListenAndServeTLS(
					server.Configuration.HTTPS.Listen,
					server.Configuration.HTTPS.SslCertificate,
					server.Configuration.HTTPS.SslCertificateKey,
					server.router.HandleFastHTTP,
				)
				if err != nil {
					log.Fatal(err)
				}
			}
		}()
	}

}

func (server *HTTPServer) BasicAuth(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	log.WithFields(log.Fields{
		"next": next,
	}).Debug("BasicAuth")

	var (
		basicAuthPrefix = []byte("Basic ")
	)
	fn := func(ctx *fasthttp.RequestCtx) {
		auth := ctx.Request.Header.Peek("Authorization")
		if bytes.HasPrefix(auth, basicAuthPrefix) {
			payload, err := base64.StdEncoding.DecodeString(string(auth[len(basicAuthPrefix):]))
			if err == nil {
				pair := bytes.SplitN(payload, []byte(":"), 2)
				if len(pair) == 2 && subtle.ConstantTimeCompare(server.requiredUser, pair[0]) == 1 && subtle.ConstantTimeCompare(server.requiredPassword, pair[1]) == 1 {
					next(ctx)
					return
				}
			}
		}

		log.Warn("Unauthorized request")

		ctx.Response.Header.Set("WWW-Authenticate", "Basic realm=Restricted")
		ctx.Error(fasthttp.StatusMessage(fasthttp.StatusUnauthorized), fasthttp.StatusUnauthorized)
	}

	return fn
}

func (server *HTTPServer) handlerCall(ctx *fasthttp.RequestCtx) {
	log.Debug("HTTPServer.handlerCall")

	msgBytes, _ := twilio.GenerateXML(
		[]string{
			"Testing bridge from grafana to G S M call",
		},
		server.Configuration.Webhooks.Twilio.Voice,
	)

	server.Notify(msgBytes)
}

func (server *HTTPServer) Notify(msgBytes []byte) {
	for _, to := range server.Configuration.Webhooks.Twilio.Notify {
		sid := RandStringRunes(10)

		server.storageMu.Lock()
		storage[sid] = string(msgBytes)
		server.storageMu.Unlock()

		queryURL := fmt.Sprintf(
			"%v/webhook/twilio/%v.xml",
			server.Configuration.ExternalEndpoint, sid,
		)

		log.WithFields(log.Fields{
			"sid":      sid,
			"queryURL": queryURL,
			"to":       to,
		}).Debug("Putting the call in queue")

		resp, err := server.Twilio.QueueCall(to, queryURL)
		if err != nil {
			log.Error(err)
			continue
		}

		logger := log.WithFields(log.Fields{
			"sid":  sid,
			"resp": resp,
			"to":   to,
		})

		if resp.Status != "queued" {
			logger.Error("the call did not hit the queue")
			continue
		}
		logger.Info("the call is queued")
	}
}

func (server *HTTPServer) getSIDFromPath(path string) (string, error) {
	log.WithFields(log.Fields{
		"path": path,
	}).Debug("HTTPServer.getSIDFromPath")

	var result string

	reqSID := regexpWebhookTwilio.FindAllStringSubmatch(path, -1)

	if len(reqSID) > 0 {
		if len(reqSID[0]) == 2 {
			result = reqSID[0][1]
		}
	}
	return result, nil
}

func (server *HTTPServer) handlerWebhookTwilio(ctx *fasthttp.RequestCtx) {
	logger := log.WithFields(log.Fields{
		"path": string(ctx.Path()),
	})
	logger.Debug("HTTPServer.handlerWebhookTwilio")

	sid, err := server.getSIDFromPath(string(ctx.Path()))

	if err != nil {
		logger.Error("error 400: invalid sid")
		ctx.Error(fasthttp.StatusMessage(fasthttp.StatusBadRequest), fasthttp.StatusBadRequest)
		return
	}

	if sid == "" {
		logger.Error("error 400: empty sid")
		ctx.Error(fasthttp.StatusMessage(fasthttp.StatusBadRequest), fasthttp.StatusBadRequest)
		return
	}

	server.storageMu.RLock()
	body, exist := storage[sid]
	server.storageMu.RUnlock()

	if !exist {
		logger.Errorf("error 404: storage[\"%v\"] not found", sid)
		ctx.Error(fasthttp.StatusMessage(fasthttp.StatusNotFound), fasthttp.StatusNotFound)
		return
	}

	ctx.Response.Header.Set("Content-type", "text/xml")
	ctx.SetBodyString(body)

	go func() {
		server.storageMu.Lock()
		delete(storage, sid)
		server.storageMu.Unlock()
	}()
}

func (server *HTTPServer) handlerWebhookGrafana(ctx *fasthttp.RequestCtx) {
	logger := log.WithFields(log.Fields{
		"method": string(ctx.Method()),
	})

	logger.Debug("HTTPServer.handlerWebhookGrafana")

	var (
		recived GrafanaHook
		err     error
	)

	err = json.Unmarshal(ctx.Request.Body(), &recived)
	if err != nil {
		logger.Errorf("json.Unmarshal error 400: %v", err)
		ctx.Error(fasthttp.StatusMessage(fasthttp.StatusBadRequest), fasthttp.StatusBadRequest)
		return
	}

	go func() {
		msgBytes, _ := twilio.GenerateXML(
			[]string{
				"",
				server.Configuration.Webhooks.Grafana.PreMessage,
				recived.Title,
				recived.Message,
			},
			server.Configuration.Webhooks.Twilio.Voice,
		)

		server.Notify(msgBytes)
	}()
}
