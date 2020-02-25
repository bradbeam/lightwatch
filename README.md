# EnvWatcher

## deployment

```bash
make deploy IMG=bradbeam/lightwatch:latest
kubectl apply -f config/samples/lightwatch_v1alpha1_envwatcher.yaml
```

Resources are created under the `lightstream-system` namespace.
