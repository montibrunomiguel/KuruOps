<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# ArgusOps no GCP (GKE)

Provisiona a infraestrutura de nuvem que o ArgusOps precisa: uma rede
VPC-native, um cluster GKE com nós privados, uma instância Cloud SQL
Postgres 16 (IP privado), um repositório Artifact Registry contendo as
quatro imagens e — opcionalmente — um bucket GCS para upload de anexos.
Isso **não** aplica os manifests de `deploy/k8s/`, não roda
`db/migrations/`, nem configura ingress/TLS/autoscaling — mesmo corte de
escopo da stack de AWS, veja a nota de escopo de `deploy/k8s/README.md`.

## Pré-requisitos

- Terraform >= 1.5
- CLI `gcloud` autenticada (`gcloud auth application-default login`), um
  projeto GCP existente com faturamento habilitado
- `kubectl`, `docker` e `psql` na sua máquina

## 1. Provisionar a infraestrutura

```bash
cd deploy/gcp
cp terraform.tfvars.example terraform.tfvars
# edite terraform.tfvars — no mínimo defina project_id

terraform init
terraform plan
terraform apply
```

`apis.tf` habilita todas as APIs do GCP que esta stack precisa no projeto
alvo, então um projeto novinho em folha funciona já no primeiro `apply` sem
precisar de um passo separado de `gcloud services enable`.

## 2. Apontar o kubectl para o novo cluster

```bash
$(terraform output -raw configure_kubectl)
kubectl get nodes
```

## 3. Fazer build e push das quatro imagens

```bash
$(terraform output -raw artifact_registry_login_command)

cd ../..   # raiz do repositório
REPO=$(cd deploy/gcp && terraform output -raw artifact_registry_repo)
for target in api ingest worker; do
  docker build --target "$target" -t "$REPO/$target:latest" -f backend/Dockerfile .
  docker push "$REPO/$target:latest"
done
docker build -t "$REPO/frontend:latest" ./frontend
docker push "$REPO/frontend:latest"
```

## 4. Criar as roles do banco de dados

O IP privado do Cloud SQL só é alcançável de dentro da VPC — a forma mais
fácil de executar os dois scripts `db/init/*.sql` a partir do seu laptop é
um túnel do
[Cloud SQL Auth Proxy](https://cloud.google.com/sql/docs/postgres/sql-proxy)
(ou execute este passo a partir de um pod/Cloud Shell já dentro da VPC):

```bash
cd deploy/gcp
cloud-sql-proxy "$(terraform output -raw db_connection_name)" &

DB_PASS=$(terraform output -raw db_master_password)
psql "postgres://postgres:$DB_PASS@127.0.0.1:5432/argusops?sslmode=disable" \
  -v app_password='CHANGE-ME-APP-PASSWORD' \
  -f ../../db/init/argusops_app_role.sql
psql "postgres://postgres:$DB_PASS@127.0.0.1:5432/argusops?sslmode=disable" \
  -v worker_password='CHANGE-ME-WORKER-PASSWORD' \
  -f ../../db/init/argusops_worker_role.sql
```

## 5. Preencher o Secret e o ConfigMap de `deploy/k8s/`

Copie `deploy/k8s/01-secret.example.yaml` e preencha:
- `DATABASE_URL_APP` / `DATABASE_URL_WORKER` — o output `db_private_ip` + as
  senhas que você acabou de definir para `argusops_app`/`argusops_worker`
- `DATABASE_URL_MIGRATE` — o output `db_private_ip` + a senha do superusuário
  `postgres` (output `db_master_password`)
- `SECRETS_ENCRYPTION_KEY` — `openssl rand -base64 32`
- `JWT_PRIVATE_KEY`/`JWT_PUBLIC_KEY` — gere conforme o próprio comentário
  daquele arquivo

Atualize o `APP_BASE_URL` de `deploy/k8s/02-configmap.yaml` para onde você
vai expor o Service do frontend (veja o passo 7).

## 6. Atualizar as referências de imagem e aplicar

Atualize o `image:` em `deploy/k8s/04-api.yaml`, `05-ingest.yaml`,
`06-worker.yaml`, `07-frontend.yaml` e o initContainer `copy-migrations` de
`03-migration-job.yaml` para `$REPO/<name>:latest` do passo 3, então siga a
ordem de aplicação de `deploy/k8s/README.md`.

## 7. Acessar a aplicação de fora do cluster

O Service do `frontend` é `ClusterIP` de propósito — não há controlador de
ingress aqui. Forma mais rápida de experimentar:

```bash
kubectl -n argusops port-forward svc/frontend 8080:8080
```

Para algo real, instale um GKE Ingress (classe de ingress GCE) ou troque o
Service do `frontend` para `type: LoadBalancer` para obter um IP externo
rapidamente.

## Desmontando tudo

```bash
cd deploy/gcp
terraform destroy
```

`deletion_protection = false` tanto no cluster quanto na instância Cloud SQL
significa que isso não deixa nada protegido para trás — não confie nisso
para nada com dados reais.

## O que isso deliberadamente não faz

Mesmo corte de escopo da stack de AWS: nenhum ingress, nenhum autoscaling,
nenhuma integração de Workload Identity (nada aqui precisa de uma
identidade GCP ambiente — a integração de uploads no GCS usa uma chave de
service account estática via Settings, veja o comentário de `storage.tf`),
cluster zonal (não regional), nenhum Cloud SQL HA. Uma amostra para
construir uma implantação de verdade a partir dela, não uma arquitetura de
referência para produção.
