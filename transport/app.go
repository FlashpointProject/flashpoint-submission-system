package transport

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kofalt/go-memoize"

	"github.com/FlashpointProject/flashpoint-submission-system/config"
	"github.com/FlashpointProject/flashpoint-submission-system/logging"
	"github.com/FlashpointProject/flashpoint-submission-system/resumableuploadservice"
	"github.com/FlashpointProject/flashpoint-submission-system/service"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"github.com/gorilla/securecookie"
	"github.com/rs/cors"
	"github.com/sirupsen/logrus"
)

// App is App
type App struct {
	Conf                *config.Config
	CC                  utils.CookieCutter
	Service             *service.SiteService
	decoder             *schema.Decoder
	authMiddlewareCache *memoize.Memoizer
	DFStorage           *DeviceFlowStorage
	AuthCodeStorage     *AuthCodeStorage
	AdminModePassword   string
}

func InitApp(l *logrus.Entry, conf *config.Config, db *sql.DB, pgdb *pgxpool.Pool, authBotSession, notificationBotSession *discordgo.Session, rsu *resumableuploadservice.ResumableUploadService) {
	l.Infoln("initializing the server")
	router := mux.NewRouter()
	host := "0.0.0.0"
	if conf.FlashpointSourceOnlyAdminMode {
		host = "127.0.0.1"
	}

	// Configure CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"http://localhost:*", "https://flashpointarchive.org", "https://*.flashpointarchive.org", "https://*.unstable.life"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})

	handler := c.Handler(router)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", host, conf.Port),
		Handler:      logging.LogRequestHandler(l, handler),
		ReadTimeout:  time.Duration(1) * time.Hour,
		WriteTimeout: time.Duration(1) * time.Hour,
	}

	decoder := schema.NewDecoder()
	decoder.ZeroEmpty(false)
	decoder.IgnoreUnknownKeys(true)

	// Generate the admin password, only used in source only admin mode
	b := make([]byte, 12)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	// Encode the byte slice as a base64 string
	adminPass := base64.StdEncoding.EncodeToString(b)
	if conf.FlashpointSourceOnlyAdminMode {
		l.WithField("admin_password", adminPass).Infoln(fmt.Sprintf("generated admin password: %s", adminPass))
	}

	a := &App{
		Conf: conf,
		CC: utils.CookieCutter{
			Previous: securecookie.New([]byte(conf.SecurecookieHashKeyPrevious), []byte(conf.SecurecookieBlockKeyPrevious)),
			Current:  securecookie.New([]byte(conf.SecurecookieHashKeyCurrent), []byte(conf.SecurecookieBlockKeyPrevious)),
		},
		AuthCodeStorage: NewAuthCodeStorage(),
		DFStorage:       NewDeviceFlowStorage(conf.HostBaseURL),
		Service: service.New(l, db, pgdb, authBotSession, notificationBotSession, conf.FlashpointServerID,
			conf.NotificationChannelID, conf.CurationFeedChannelID, conf.ValidatorServerURL, conf.SessionExpirationSeconds,
			conf.SubmissionsDirFullPath, conf.SubmissionImagesDirFullPath, conf.FlashfreezeDirFullPath, conf.IsDev,
			rsu, conf.ArchiveIndexerServerURL, conf.FlashfreezeIngestDirFullPath,
			conf.DataPacksDir),
		decoder:             decoder,
		authMiddlewareCache: memoize.NewMemoizer(5*time.Second, 60*time.Minute),
		AdminModePassword:   adminPass,
	}

	l.WithField("port", conf.Port).Infoln("starting the server...")

	go func() {
		a.handleRequests(l, srv, router)
	}()

	ctx, cancelFunc := context.WithCancel(context.Background())

	wg := &sync.WaitGroup{}

	l.Infoln("starting the data pack indexer...")

	a.Service.DataPacksIndexer.Start()

	if !conf.FlashpointSourceOnlyMode {
		l.Infoln("starting the notification consumer...")

		wg.Add(1)
		go func() {
			a.Service.RunNotificationConsumer(l, ctx, wg)
		}()

		l.Infoln("starting the autounfreezer...")
		wg.Add(1)
		go func() {
			a.Service.RunAutounfreezer(l, ctx, wg, a.Conf.DataPacksDir, a.Conf.FrozenPacksDir, a.Conf.DeletedDataPacksDir, a.Conf.DoNotUnfreezeGameList)
		}()
	}

	// disable memstats for now
	//l.Infoln("starting the memstats printer...")
	//wg.Add(1)
	//go memstatsPrinter(l, ctx, wg)

	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-term
	l.Infoln("signal received")

	l.Infoln("waiting for all goroutines to finish...")
	cancelFunc()
	wg.Wait()

	if !conf.FlashpointSourceOnlyMode {
		l.Infoln("closing the auth bot session...")
		authBotSession.Close()

		l.Infoln("closing the notification bot session...")
		notificationBotSession.Close()
	}

	l.Infoln("closing data pack indexer...")
	a.Service.DataPacksIndexer.Stop()

	l.Infoln("shutting down the server...")
	if err := srv.Shutdown(context.Background()); err != nil {
		l.WithError(err).Errorln("server shutdown failed")
	}

	l.Infoln("goodbye")
}

func memstatsPrinter(l *logrus.Entry, ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	defer l.Infoln("memstats printer stopped")

	bucket, ticker := utils.NewBucketLimiter(60*time.Second, 1)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			l.Infoln("context cancelled, stopping memstats printer")
			return
		case <-bucket:
			m := utils.GetMemStats()
			l.WithFields(logrus.Fields{"alloc": m.Alloc, "sys": m.Sys, "num_gc": m.NumGC, "heap_objects": m.HeapObjects, "gc_cpu_fraction": fmt.Sprintf("%.6f", m.GCCPUFraction)}).Debug("memstats")
		}
	}
}
