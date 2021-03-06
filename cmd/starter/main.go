package main

import (
	"os"
	"runtime"
	"strings"

	"gopkg.in/polds/logrus-papertrail-hook.v2"

	"launchpad.net/goamz/aws"
	"launchpad.net/goamz/s3"

	"github.com/Luzifer/gobuilder/buildjob"
	"github.com/Luzifer/gobuilder/config"
	"github.com/Sirupsen/logrus"
	"github.com/cenkalti/backoff"
	"github.com/fsouza/go-dockerclient"
	"github.com/robfig/cron"
	"github.com/xuyu/goredis"
)

var (
	dockerClient        *docker.Client
	log                 = logrus.New()
	s3Bucket            *s3.Bucket
	redisClient         *goredis.Redis
	currentJobs         chan bool
	conf                *config.Config
	version             = "dev"
	killswitch          = false
	hostname            = "unkown"
	maxConcurrentBuilds = runtime.NumCPU()
)

const (
	maxJobRetries = 2
)

func init() {
	var err error
	log.Out = os.Stderr

	conf = config.Load()

	// Add Papertrail connection for logging
	if conf.Papertrail.Port != 0 {
		hook, err := logrus_papertrail.NewPapertrailHook(&logrus_papertrail.Hook{
			Host:     conf.Papertrail.Host,
			Port:     conf.Papertrail.Port,
			Hostname: "GoBuilder",
			Appname:  "Starter",
		})
		if err != nil {
			log.WithFields(logrus.Fields{
				"host": hostname,
			}).Panic("Unable to create papertrail connection")
			os.Exit(1)
		}

		log.Hooks.Add(hook)
	} else {
		log.WithFields(logrus.Fields{
			"host": hostname,
		}).Info("Failed to read papertrail_port, using only STDERR")
	}

	connectRedis()

	awsAuth, err := aws.EnvAuth()
	if err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
		}).Panic("Unable to read AWS credentials")
		os.Exit(1)
	}
	s3Bucket = s3.New(awsAuth, aws.EUWest).Bucket("gobuild.luzifer.io")

	dockerClient, err = docker.NewClient("unix:///var/run/docker.sock")
	if err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
		}).Panic("Unable to connect to docker daemon")
		os.Exit(1)
	}

	currentJobs = make(chan bool, maxConcurrentBuilds)

	hostname, err = os.Hostname()
}

func connectRedis() {
	var err error
	redisClient, err = goredis.DialURL(conf.RedisURL)
	if err != nil {
		log.WithFields(logrus.Fields{
			"url":  conf.RedisURL,
			"host": hostname,
		}).Panic("Unable to connect to Redis")
		os.Exit(1)
	}
}

func main() {
	if err := backoff.Retry(pullLatestImage, backoff.NewExponentialBackOff()); err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
		}).Panic("Unable to fetch docker image for builder")
		os.Exit(1)
	}

	log.WithFields(logrus.Fields{
		"host": hostname,
	}).Infof("Build starter version %s with %d build slots in service.", version, maxConcurrentBuilds)

	c := cron.New()
	c.AddFunc("0 * * * * *", announceActiveWorker)
	c.AddFunc("0 */30 * * * *", func() {
		err := pullLatestImage()
		if err != nil {
			log.WithFields(logrus.Fields{
				"host":  hostname,
				"error": err,
			}).Error("Unable to refresh build image")
		}
	})
	c.AddFunc("*/10 * * * * *", func() {
		if !killswitch {
			go doBuildProcess()
		} else {
			if len(currentJobs) == 0 {
				os.Exit(0)
			}
		}
	})
	c.Start()

	for {
		select {}
	}
}

func doBuildProcess() {
	if len(currentJobs) == maxConcurrentBuilds {
		// If maxConcurrentBuilds are running, do not fetch more jobs
		return
	}

	currentJobs <- true
	defer func() {
		<-currentJobs
	}()

	queueLength, err := redisClient.LLen("build-queue")
	if err != nil {
		if strings.Contains(err.Error(), "broken pipe") {
			// Somehow we lost connection to redis, try reconnecting
			connectRedis()
		}

		log.WithFields(logrus.Fields{
			"host":  hostname,
			"error": err.Error(),
		}).Error("Unable to determine queue length.")
		return
	}

	if queueLength < 1 {
		// There is no job? Stop now.
		return
	}

	body, err := redisClient.LPop("build-queue")
	if err != nil {
		log.WithFields(logrus.Fields{
			"host":  hostname,
			"error": err.Error(),
		}).Error("An error occurred while getting job")
		return
	}

	job, err := buildjob.FromBytes(body)
	if err != nil {
		// there was a job we could not parse throw it away and stop here
		return
	}

	builder := newBuilder(job)

	// Try to get the lock for this job and quit if we don't get it
	if builder.AquireLock() != nil {
		builder.PutBackJob(false)
		return
	}

	// Prepare everything for the build or put back the job and stop if we can't
	if err = builder.PrepareBuild(conf.TmpDir); err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
			"repo": builder.job.Repository,
		}).Error("PrepareBuild failed")

		builder.PutBackJob(false)
		return
	}

	// Ensure we don't make a mess after we're done
	defer builder.Cleanup()

	// Do the real build
	if err := builder.Build(); err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
			"repo": builder.job.Repository,
		}).Error("Build failed")

		builder.PutBackJob(true)
		return
	}

	// Handle the build log
	if err := builder.FetchBuildLog(); err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
			"repo": builder.job.Repository,
		}).Error("Was unable to fetch the log from the container")

		builder.PutBackJob(false)
		return
	}

	if err := builder.WriteBuildLog(); err != nil {
		log.WithFields(logrus.Fields{
			"host": hostname,
			"err":  err,
			"repo": builder.job.Repository,
		}).Error("Was unable to store the build log")
	}

	// If the build was marked as failed abort now
	if !builder.BuildOK {
		ib, _ := builder.IsBuildable()

		if ib {
			log.WithFields(logrus.Fields{
				"host": hostname,
				"repo": builder.job.Repository,
			}).Error("Build was marked as failed, requeuing now.")
			builder.PutBackJob(true)
		} else {
			log.WithFields(logrus.Fields{
				"host": hostname,
				"repo": builder.job.Repository,
			}).Errorf("Build failed and is not buildable: %s", builder.AbortReason)
			builder.UpdateBuildStatus(BuildStatusFailed, 0)
		}

		return
	}

	// Handle the uploads
	if builder.UploadRequired {
		if err := builder.UploadAssets(); err != nil {
			log.WithFields(logrus.Fields{
				"host": hostname,
				"err":  err,
				"repo": builder.job.Repository,
			}).Error("Was unable to upload the build assets")

			builder.PutBackJob(false)
			return
		}
	}

	builder.UpdateBuildStatus(BuildStatusFinished, 0)

	if builder.UploadRequired {
		if err := builder.UpdateMetaData(); err != nil {
			log.WithFields(logrus.Fields{
				"host": hostname,
				"err":  err,
				"repo": builder.job.Repository,
			}).Error("There was an error while updating metadata")

			builder.PutBackJob(false)
			return
		}
	}

	// Send success notifications
	builder.SendNotifications()
	builder.TriggerSubBuilds()
}
