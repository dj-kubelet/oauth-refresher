package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	labelSelector string
	clientID      string
	clientSecret  string
	authURL       string
	tokenURL      string
	conf          *oauth2.Config
	clientset     *kubernetes.Clientset
)

func setup() {
	flag.StringVar(&labelSelector, "labelSelector", "dj-kubelet.com/oauth-refresher=spotify", "")
	flag.Parse()

	ok := false
	clientID, ok = os.LookupEnv("CLIENT_ID")
	if !ok {
		log.Fatalln("env CLIENT_ID not set")
	}
	clientSecret, ok = os.LookupEnv("CLIENT_SECRET")
	if !ok {
		log.Fatalln("env CLIENT_SECRET not set")
	}
	authURL, ok = os.LookupEnv("AUTH_URL")
	if !ok {
		log.Fatalln("env AUTH_URL not set")
	}
	tokenURL, ok = os.LookupEnv("TOKEN_URL")
	if !ok {
		log.Fatalln("env TOKEN_URL not set")
	}

	conf = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authURL,
			TokenURL: tokenURL,
		},
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		log.Println("Failed to set up in cluster configuration, testing with kubeconfig")
		kubeconfig := os.Getenv("KUBECONFIG")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.Fatalln(err.Error())
		}
	}

	clientset = kubernetes.NewForConfigOrDie(config)
}

type patchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func refresh() {
	namespaces, err := clientset.CoreV1().Namespaces().List(metav1.ListOptions{})
	if err != nil {
		log.Printf("Failed to list namespaces: %+v", err)
		return
	}

	for _, ns := range namespaces.Items {
		secrets, err := clientset.CoreV1().Secrets(ns.Name).List(metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			log.Printf("Failed to list secrets: %+v", err)
			continue
		}
		for _, secret := range secrets.Items {
			refreshSingle(secret)
		}
	}
}

func refreshSingle(secret apiv1.Secret) {
	log.Printf("Starting refresh of: %s/%s", secret.Namespace, secret.Name)

	// Reconstruct Oauth2 object that has expired
	token := &oauth2.Token{
		AccessToken:  string(secret.Data["accesstoken"]),
		RefreshToken: string(secret.Data["refreshtoken"]),
		Expiry:       time.Now(),
	}

	newToken, err := conf.TokenSource(oauth2.NoContext, token).Token()
	if err != nil {
		log.Fatalln(err)
	}

	if newToken.AccessToken != token.AccessToken {
		token = newToken
		log.Println("Access token has changed")
	}

	if clientset != nil {
		patch := []patchOperation{
			patchOperation{
				Op:    "add",
				Path:  "/stringData",
				Value: make(map[string]string),
			},
			patchOperation{
				Op:    "add",
				Path:  "/stringData/accesstoken",
				Value: token.AccessToken,
			},
			patchOperation{
				Op:    "add",
				Path:  "/stringData/refreshtoken",
				Value: token.RefreshToken,
			},
			patchOperation{
				Op:    "add",
				Path:  "/stringData/expiry",
				Value: token.Expiry.Format(time.RFC3339),
			},
			patchOperation{
				Op:    "add",
				Path:  "/stringData/updated",
				Value: time.Now().Format(time.RFC3339),
			},
		}
		raw, err := json.Marshal(patch)
		if err != nil {
			fmt.Println(err)
		}
		fin, err := clientset.CoreV1().Secrets(secret.Namespace).Patch(secret.Name, types.JSONPatchType, raw)
		if err == nil {
			log.Printf("Patched secret %s/%s", secret.Namespace, secret.Name)
		} else {
			fmt.Println(err)
			fmt.Println(fin)
		}
	}
}

func main() {
	setup()

	// perform initial refresh
	refresh()

	ticker := time.NewTicker(10 * time.Minute)
	quit := make(chan bool)
	go func() {
		for {
			select {
			case <-ticker.C:
				refresh()
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	log.Println("Shutdown signal received, exiting...")

	quit <- true
}
