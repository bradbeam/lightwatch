# EnvWatcher

`EnvWatcher` is a Kubernetes controller that will watch a target URL for a given interval and create a configmap with the target URL contents.

## CRD

```yaml
apiVersion: lightwatch.vigilant.dev/v1alpha1
kind: EnvWatcher
metadata:
  name: envwatcher-sample
  namespace: lightstream-system
spec:
  url: <target url>
  frequency: 1h
```

## deployment

```bash
make deploy IMG=bradbeam/lightwatch:latest
kubectl apply -f config/samples/lightwatch_v1alpha1_envwatcher.yaml
```

Resources are created under the `lightstream-system` namespace.

## Slides

A [present](https://github.com/golang/tools) slide deck can be found [here](docs/lightwatch.slide).

[Present](https://github.com/golang/tools) can be downloaded via `go get golang.org/x/tools/cmd/present`.

## Examples
Deploy 10 CRDs

```bash
for i in $(seq 1 10); do sed -e 's/envwatcher-sample/envwatcher-sample-'$i'/g' config/samples/lightwatch_v1alpha1_envwatcher.yaml | kubectl apply -f - ;  done
# hang out for a few to wait for downloads to complete,etc
sleep 5
kubectl get deployment,envwatcher,configmap -n lightstream-system
for i in $(seq 1 10); do sed -e 's/envwatcher-sample/envwatcher-sample-'$i'/g' config/samples/lightwatch_v1alpha1_envwatcher.yaml | kubectl delete -f - ;  done
```

