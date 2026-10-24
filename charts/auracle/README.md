# auracle Helm chart

Runs a single instance of the auracle Discord bot. The Discord token is read from an existing Kubernetes Secret, and the files the bot answers from are mounted read-only from a PersistentVolumeClaim.

The bot only makes outbound connections (to Discord and Ollama), so the chart creates no Service or Ingress.

## 1. Build and push the image

From the repo root:

```sh
docker buildx build --platform linux/amd64,linux/arm64 \
  -t ghcr.io/aufishgamelabs/auracle:0.1.0 --push .
```

Building for both platforms matters if you build on an Apple Silicon Mac but your cluster runs on amd64 nodes. If you push somewhere else, set `image.repository` to match. If the registry is private, add a pull secret with `imagePullSecrets`.

## 2. Create the Discord token Secret

```sh
kubectl create secret generic auracle-discord --from-literal=token=YOUR_BOT_TOKEN
```

The token never goes in the chart's values. The chart only refers to the Secret by name.

## 3. Install

```sh
helm install auracle ./charts/auracle \
  --set discord.existingSecret=auracle-discord \
  --set ollama.url=http://ollama.ollama.svc:11434 \
  --set ollama.model=gpt-oss:120b
```

Set `ollama.url` to wherever Ollama is reachable from inside the cluster.

Check that it connected:

```sh
kubectl logs deploy/auracle
```

## 4. Load the docs

### From a directory on the node

Set `docs.hostPath.path` to an absolute path on the node (Helm does not expand `~`). The chart creates a `hostPath` PersistentVolume for it and a claim bound to that volume, and you add or edit files in the directory directly:

```sh
helm upgrade --install auracle ./charts/auracle \
  --set discord.existingSecret=auracle-discord \
  --set docs.hostPath.path=$HOME/llms/auracle
```

`make helm` does this with `~/llms/auracle` by default. The directory must already exist, and on a multi-node cluster it must be on the node the bot runs on (use `nodeSelector`). The volume's reclaim policy is `Retain`, so uninstalling never touches the files.

### In a PersistentVolumeClaim

By default the chart creates a 1Gi PersistentVolumeClaim named `<release>-auracle-docs` (just `auracle-docs` if the release is named `auracle`). The bot sees new files without restarting.

The bot ignores files and folders whose names start with `.` or `_`, so you can keep drafts or an archive next to the docs (e.g. `_archive/`).

Convert any PDFs to Markdown first with [`pdf2md`](../../README.md#converting-pdfs-pdf2md), since the bot can't read PDFs.

The bot's image has no shell, so copy files in with a temporary helper pod:

```sh
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: auracle-loader
spec:
  securityContext:
    runAsUser: 65532
    runAsGroup: 65532
    fsGroup: 65532
  containers:
    - name: loader
      image: busybox:1.36
      command: ["sleep", "3600"]
      volumeMounts:
        - name: docs
          mountPath: /docs
  volumes:
    - name: docs
      persistentVolumeClaim:
        claimName: auracle-docs
EOF

kubectl wait --for=condition=Ready pod/auracle-loader
kubectl cp ./World-Eddiair-dd7 auracle-loader:/docs/
kubectl delete pod auracle-loader
```

With the default `ReadWriteOnce` access mode, the helper pod must be scheduled on the same node as the bot. Kubernetes normally handles this, but if the helper pod stays `Pending`, either scale the bot down while loading (`kubectl scale deploy/auracle --replicas=0`, then back to `1`) or use a `ReadWriteMany` storage class.

The claim is annotated with `helm.sh/resource-policy: keep`, so `helm uninstall` leaves your docs in place.

To use docs you already have in a volume, set `docs.existingClaim` (and `docs.subPath` to use one folder within it) instead.

## Values

| Value | Default | Description |
|-------|---------|-------------|
| `discord.existingSecret` | `""` | **Required.** Name of the Secret holding the Discord bot token. |
| `discord.tokenKey` | `token` | Key within that Secret. |
| `ollama.url` | `http://ollama:11434` | Ollama server URL, as reachable from the pod. |
| `ollama.model` | `gpt-oss:120b` | Model name. Must support tool calling. |
| `docs.existingClaim` | `""` | Use this existing PersistentVolumeClaim for the docs. |
| `docs.subPath` | `""` | Folder within the volume to use as the docs directory. |
| `docs.hostPath.path` | `""` | Absolute path of a directory on the node to use as the docs store. Takes precedence over `persistence`. |
| `docs.hostPath.size` | `1Gi` | Capacity recorded on the hostPath volume and claim. |
| `docs.persistence.enabled` | `true` | Create a claim for the docs when neither `existingClaim` nor `hostPath.path` is set. |
| `docs.persistence.storageClass` | `""` | StorageClass for the created claim. Empty uses the cluster default. |
| `docs.persistence.accessModes` | `[ReadWriteOnce]` | Access modes for the created claim. |
| `docs.persistence.size` | `1Gi` | Size of the created claim. |
| `systemPrompt` | `""` | Extra instructions appended to the bot's built-in ones. |
| `maxHistory` | `20` | Messages of history kept per channel or whispering user. |
| `log.level` | `info` | `debug`, `info`, `warn` or `error`. `debug` logs question and answer text. |
| `log.format` | `text` | `text` or `json`. |
| `extraEnv` | `[]` | Extra environment variables for the container. |
| `image.repository` | `ghcr.io/aufishgamelabs/auracle` | Image to run. |
| `image.tag` | chart `appVersion` | Image tag. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Pull secrets for a private registry. |
| `resources` | 10m CPU / 32Mi requested, 256Mi limit | Container resources. The bot itself is light; the model runs in Ollama. |
| `podAnnotations`, `podLabels` | `{}` | Extra pod metadata. |
| `nodeSelector`, `tolerations`, `affinity` | empty | Pod scheduling. |

## Single instance

The chart always runs exactly one replica, with the `Recreate` update strategy so the old pod stops before a new one starts. Two instances connected at once would both answer every question.

Conversation history is kept in memory, so it resets whenever the pod restarts or is upgraded.
