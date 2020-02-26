# EnvWatcher

## deployment

```bash
make deploy IMG=bradbeam/lightwatch:latest
kubectl apply -f config/samples/lightwatch_v1alpha1_envwatcher.yaml
```

Resources are created under the `lightstream-system` namespace.

## Examples
```bash
for i in $(seq 1 10); do sed -e 's/envwatcher-sample/envwatcher-sample-'$i'/g' config/samples/lightwatch_v1alpha1_envwatcher.yaml | kubectl apply -f - ;  done
# hang out for a few to wait for downloads to complete,etc
sleep 5
kubectl get deployment,envwatcher,configmap -n lightstream-system
for i in $(seq 1 10); do sed -e 's/envwatcher-sample/envwatcher-sample-'$i'/g' config/samples/lightwatch_v1alpha1_envwatcher.yaml | kubectl delete -f - ;  done
```
