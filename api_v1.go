package main

import (
	"fmt"
	"net/http"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/mux"
)

func registerAPIv1(router *mux.Router) {
	r := router.PathPrefix("/api/v1/").Subrouter()

	// Add build starters
	r.HandleFunc("/build", webhookInterface).Methods("POST")
	r.HandleFunc("/webhook/github", webhookGitHub).Methods("POST")
	r.HandleFunc("/webhook/bitbucket", webhookBitBucket).Methods("POST")

	r.HandleFunc("/{repo:.+}/last-build", apiV1HandlerLastBuild).Methods("GET")
}

func apiV1HandlerLastBuild(res http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	redisKey := fmt.Sprintf("project::%s::last-build", vars["repo"])
	lastBuild, err := redisClient.Get(redisKey)
	if err != nil {
		log.WithFields(logrus.Fields{
			"error": err,
			"repo":  vars["repo"],
		}).Error("Failed to get last build hash")
		http.Error(res, "Could not read last build hash", http.StatusInternalServerError)
		return
	}

	res.Header().Add("Content-Type", "text/plain")
	res.Header().Add("Cache-Control", "no-cache")
	res.Write(lastBuild)
}