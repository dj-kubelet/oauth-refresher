package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/oauth2"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	labelKey        string
	clientID        string
	clientSecret    string
	authURL         string
	tokenURL        string
	conf            *oauth2.Config
	clientset       *kubernetes.Clientset
	refreshInterval int
)

func setup() {
	flag.StringVar(&labelKey, "labelKey", "dj-kubelet.com/oauth-refresher", "")
	flag.IntVar(&refreshInterval, "refreshInterval", 600, "")
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

func createSecretInformer(factory informers.SharedInformerFactory, resyncPeriod time.Duration, filter func(*apiv1.Secret) bool, onUpdate func(*apiv1.Secret)) cache.SharedIndexInformer {
	informer := factory.Core().V1().Secrets().Informer()
	informer.AddEventHandlerWithResyncPeriod(cache.FilteringResourceEventHandler{
		FilterFunc: func(obj interface{}) bool {
			return filter(obj.(*apiv1.Secret))
		},
		Handler: cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				onUpdate(obj.(*apiv1.Secret))
			},
			UpdateFunc: func(_, obj interface{}) {
				onUpdate(obj.(*apiv1.Secret))
			},
		}}, resyncPeriod)
	return informer
}

type SecretData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	Updated      time.Time `json:"updated"`
}

func refreshSingle(secret *apiv1.Secret) {
	log.Printf("Starting refresh of: %s/%s", secret.Namespace, secret.Name)

	// Reconstruct an Oauth2 object
	token := &oauth2.Token{
		AccessToken:  string(secret.Data["access_token"]),
		RefreshToken: string(secret.Data["refresh_token"]),
		Expiry:       time.Now(),
	}

	newToken, err := conf.TokenSource(context.TODO(), token).Token()
	if err != nil {
		log.Println(err)
		return
	}

	if newToken.AccessToken != token.AccessToken {
		token = newToken
		log.Printf("Access token updated in %s/%s", secret.Namespace, secret.Name)
	}

	secretData := SecretData{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
		Updated:      time.Now(),
	}
	raw, err := json.Marshal(struct {
		StringData SecretData `json:"stringData"`
	}{secretData})
	if err != nil {
		fmt.Println(err)
		return
	}
	fin, err := clientset.CoreV1().Secrets(secret.Namespace).Patch(context.TODO(), secret.Name, types.StrategicMergePatchType, raw, metav1.PatchOptions{})
	if err == nil {
		log.Printf("Patched secret %s/%s", secret.Namespace, secret.Name)
	} else {
		fmt.Println(err)
		fmt.Println(fin)
		return
	}
}

func main() {
	setup()

	factory := informers.NewSharedInformerFactory(clientset, time.Hour*24)
	secretInformer := createSecretInformer(factory, time.Second*time.Duration(refreshInterval), func(secret *apiv1.Secret) bool {
		if _, ok := secret.ObjectMeta.Labels[labelKey]; !ok {
			return false
		}
		t, err := time.Parse(time.RFC3339, string(secret.Data["updated"]))
		if err != nil {
			fmt.Println(err)
		}
		// One mintute cooldown to avoid infinite update loops
		if time.Since(t) < time.Second*60 {
			return false
		}
		log.Printf("Secret %s/%s updated %s ago.", secret.Namespace, secret.Name, time.Since(t))
		return true
	}, refreshSingle)
	stopper := make(chan struct{})
	defer close(stopper)
	defer runtime.HandleCrash()
	go secretInformer.Run(stopper)
	if !cache.WaitForCacheSync(stopper, secretInformer.HasSynced) {
		runtime.HandleError(fmt.Errorf("Timed out waiting for caches to sync"))
		return
	}
	<-stopper
}
