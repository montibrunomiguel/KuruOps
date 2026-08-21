<p align="right"><a href="README.md">🇺🇸 English</a> · <b>🇧🇷 Português</b></p>

# Implantando o ArgusOps

Duas camadas, mantidas separadas de propósito:

- **`k8s/`** — os manifests reais do Kubernetes para a aplicação em si
  (api/ingest/worker/frontend + o Job de migração avulso). Agnóstico de
  nuvem; funciona contra qualquer cluster que já tenha um Postgres 16
  acessível. Veja `k8s/README.md` para saber o que tem ali, o que
  deliberadamente não tem (ingress, TLS, autoscaling) e a ordem de aplicação.
- **`aws/`, `gcp/`, `azure/`** — amostras de Terraform que provisionam a
  infraestrutura de nuvem que os manifests de `k8s/` assumem: uma VPC/VNet,
  um cluster Kubernetes gerenciado, uma instância de Postgres gerenciada e um
  registro de containers. Cada uma é independente (escolha uma nuvem, não as
  três) e termina com a mesma entrega: preencha `k8s/01-secret.example.yaml`
  e `02-configmap.yaml` com os detalhes de conexão daquela nuvem, então
  aplique `k8s/` seguindo o próprio README.

## Escolhendo uma nuvem

| | AWS | GCP | Azure |
|---|---|---|---|
| Cluster | EKS | GKE (nós privados) | AKS |
| Postgres gerenciado | RDS | Cloud SQL (IP privado) | Postgres Flexible Server (integrado à VNet) |
| Registro | ECR (um repo por imagem) | Artifact Registry (um repo) | ACR (um registro) |
| Armazenamento de upload da app | S3 (opcional, `internal/blobstore` já suporta) | GCS (opcional, mesma coisa) | nenhum — `internal/blobstore` ainda não tem backend de Azure Blob; permanece no PVC local com `replicas: 1`, veja `azure/README.pt-BR.md` |

Cada `<cloud>/README.md` tem o passo a passo completo (provisionar → build/push
das imagens → criar as roles de banco `argusops_app`/`argusops_worker` →
preencher o Secret/ConfigMap → aplicar `k8s/`).

## O que nada disso inclui

Alinhado ao escopo do próprio `k8s/README.md`: nenhum controlador de ingress,
nenhuma automação de TLS/certificado, nenhum HorizontalPodAutoscaler e
nenhuma integração de CI/CD. Isso entrega um cluster funcional + banco de
dados + registro provisionados e a aplicação rodando dentro dele — colocar
por cima um ingress de verdade, autoscaling e um pipeline de deploy fica a
cargo do que sua organização já usa para isso, seguindo o mesmo raciocínio
que `k8s/README.md` usa para fazer o mesmo corte.

## Antes de rodar `terraform apply` em qualquer um destes

O state do Terraform de cada diretório de nuvem vai conter segredos em texto
plano (uma senha master de banco gerada, uma access key de S3/GCS) assim que
aplicado — o `.gitignore` já exclui `*.tfstate`/`*.tfvars`, mas para qualquer
coisa além de um teste local avulso, configure um backend remoto de verdade
(S3+DynamoDB, GCS ou Azure Blob) no `versions.tf` daquela nuvem antes de
rodar `terraform apply` — state local em um laptop é aceitável para
experimentar, não para algo que um time precisa reaplicar continuamente.
