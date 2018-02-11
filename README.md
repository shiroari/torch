# TORCH

Torch is Prometheus sidecar container that fetches alerting and recording rules from services and uploads them to Prometheus in runtime.

## Build

```
$ docker build . -t torch
```

## Run on Kubernetes

```
$ kubectl apply -f examples/prom-dc.yaml
```


