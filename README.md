# OAuth-refresher

OAuth-refresher is a Kubernetes native background process that automatically updates OAuth 2.0 tokens before they expire.

The ambition is to enable dumb OAuth clients by offloading the token renewal.


## Usage

With a secret like this:
```
apiVersion: v1
kind: Secret
metadata:
  name: my-token
  labels:
    dj-kubelet.com/oauth-refresher: spotify
type: Opaque
data:
  accesstoken: aGVsbG8K
  refreshtoken: d29ybGQK

```
You'd run `oauth-refresher` with a matching `labelSelector` to have it refresh the token every 10 minutes.

```bash
./oauth-refresher --labelSelector=dj-kubelet.com/oauth-refresher=spotify --refreshInterval=600
```

Configuration of the OAuth 2.0 client is passed with environment variables.

```bash
AUTH_URL=https://accounts.spotify.com/authorize
TOKEN_URL=https://accounts.spotify.com/api/token
CLIENT_ID=aaa
CLIENT_SECRET=aaa
```


## Deploy

```bash
# Build image
docker build -t oauth-refresher .

# Load image to kind nodes
kind load docker-image --name dj-kubelet oauth-refresher

# Create namespace and apply kustomized deployment
kubectl create namespace oauth-refresher
kubectl apply -k ./development
```
