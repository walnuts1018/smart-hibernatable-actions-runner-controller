variable "REGISTRY" {
  default = "ghcr.io"
}

variable "REPO" {
  default = "walnuts1018/smart-hibernatable-actions-runner-controller"
}

variable "RELEASE_TAG" {
  default = "dev"
}

variable "ARCH_KEY" {
  default = "linux-amd64"
}

variable "PLATFORM" {
  default = "linux/amd64"
}

group "default" {
  targets = ["manager", "listener"]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = [PLATFORM]
}

target "manager" {
  inherits = ["_common"]
  target   = "manager"
  output = [
    "type=image,name=${REGISTRY}/${REPO}/manager,push-by-digest=true,name-canonical=true,push=true,compression=zstd"
  ]
  cache-from = [
    "type=gha,scope=manager-${ARCH_KEY}"
  ]
  cache-to = [
    "type=gha,mode=max,scope=manager-${ARCH_KEY},ignore-error=true"
  ]
}

target "listener" {
  inherits = ["_common"]
  target   = "listener"
  output = [
    "type=image,name=${REGISTRY}/${REPO}/listener,push-by-digest=true,name-canonical=true,push=true,compression=zstd"
  ]
  cache-from = [
    "type=gha,scope=listener-${ARCH_KEY}"
  ]
  cache-to = [
    "type=gha,mode=max,scope=listener-${ARCH_KEY},ignore-error=true"
  ]
}
