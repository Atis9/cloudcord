# Kubernetesへのデプロイ

構成は [arsis](https://github.com/Atis9/arsis) と [k8s.chitoku.jp](https://github.com/chitoku-k/k8s.chitoku.jp) のGitHub OIDC認証に合わせている。配置先は `nyantech` namespace。CI/CDで作成するイメージはamd64向けで、Deploymentはamd64ノードを選択する。

## 初回の準備

1. [セットアップ](setup.md)のGitHub `production` Environmentに必要なSecrets / Variablesを設定する。
2. クラスタ管理者が既存の `nyantech` namespaceと `nyantech-admin` Roleを確認し、RoleBindingとNetworkPolicyを適用する。

   ```sh
   kubectl apply -f kubernetes/role-binding.yaml -f kubernetes/network-policy.yaml
   ```

   NetworkPolicyの作成・更新にはクラスタ管理権限が必要。`Deploy Bot` はこのPolicyの存在を確認する。

   GitHubの `Atis9/cloudcord` リポジトリを示すOIDCグループに、このnamespaceのRoleを割り当てる。GitHub Actionsには `id-token: write` が必要。audienceは `k8s.chitoku.jp`。
3. GHCRイメージをノードから取得できるようにする。公開リポジトリでもGHCRパッケージの公開設定は別なので確認する。privateイメージを使う場合はクラスタ側でimage pull secretを用意し、Deploymentの `imagePullSecrets` に指定する。GitHub Actionsの一時tokenを長期のpull secretにしない。

## イメージの作成

`CI` の成功後、`Publish Bot` を `main` から手動実行する。同じコミットの成功したCIが生成したamd64イメージを再利用し、GHCRに `sha-<commit>` のイメージを作成する。成功時のSummaryに、イメージのdigestを表示する。CI成果物の保存期間は7日。期限切れの場合は同じ `main` でCIを再実行してから配布する。

## デプロイ

`Deploy Bot` を `main` から手動実行し、`image_digest` に `sha256:...` を指定する。ワークフローはOIDCでクラスタへ接続し、SecretとDeploymentを更新してrolloutを待つ。可変タグではなくdigestで固定する。

`kubernetes/deployment.yaml` の `VERSION` はテンプレート。手動でデプロイする場合も実際のdigestに置き換える。

```sh
kubectl set image --local -f kubernetes/deployment.yaml \
  cloudcord=ghcr.io/atis9/cloudcord@sha256:IMAGE_DIGEST -o yaml > /tmp/cloudcord-deployment.yaml
kubectl apply -f /tmp/cloudcord-deployment.yaml
kubectl -n nyantech rollout status deployment/cloudcord --timeout=180s
```

Secretを手動で作成する場合は、値を設定した `.env` を使い、標準出力を直接kubectlへ渡す。

```sh
node --env-file=.env scripts/kubernetes-secret.mjs \
  | kubectl apply --server-side --field-manager=cloudcord-deploy -f -
```

同じDiscord tokenのBotをMacや別クラスタで同時に起動しない。切り替え時は既存Botを停止する。Pod更新は `Recreate`、replicasは1。起動後はDiscordで `/join` を再実行する。

## 運用

```sh
kubectl -n nyantech get deployment,pods -l app=cloudcord
kubectl -n nyantech logs deployment/cloudcord --tail=100
kubectl -n nyantech rollout undo deployment/cloudcord
```

Podは非root・読み取り専用root filesystemで動作し、書き込み先はメモリ上の `/tmp` のみ。Kubernetes API tokenはマウントしない。外向きのHTTPSとDiscord音声用UDPが必要で、Service・Ingressは不要。`kubernetes/network-policy.yaml` は `app: cloudcord` のPodに外向きTCP 443のみを追加許可する。音声用UDPは接続先ポートを確認後、必要な範囲だけ管理者が追加する。それまではボイスチャンネルの音声接続は利用できない。プライベート・ループバック・リンクローカル宛てはこの許可から除外し、DNSなどnamespace内の既存許可と組み合わせる。

初期リソース設定はrequest 50m CPU / 64Mi、limit 500m CPU / 256Mi。実際の利用状況に応じて調整する。再起動時はセッションと待機音声を引き継がない。

arm64向けは対象のローカル環境で `docker build -t cloudcord:local .` を実行する。Kubernetesでそのイメージを使う場合は、イメージの配置先と `nodeSelector` を合わせる。
