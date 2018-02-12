# TORCH

Torch is Prometheus sidecar container that fetches alerting and recording rules from services and uploads them to Prometheus in runtime.

![alt text](https://github.com/shiroari/torch/raw/master/docs/images/arch.png)

## Build

```
$ docker build . -t torch
```

## Run on Kubernetes

```
$ kubectl apply -f examples/prom-dc.yaml
```


