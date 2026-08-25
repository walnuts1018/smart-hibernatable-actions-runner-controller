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
  targets = ["manager", "listener", "runner-hook"]
}

target "_common" {
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = [PLATFORM]
  cache-from = [
    "type=gha,scope=build-${ARCH_KEY}"
  ]
}

target "manager" {
  inherits = ["_common"]
  target   = "manager"
  cache-to = [
    "type=gha,mode=max,scope=build-${ARCH_KEY},ignore-error=true"
  ]
  output = [
    "type=image,name=${REGISTRY}/${REPO}/manager,push-by-digest=true,name-canonical=true,push=true,compression=zstd"
  ]
}

target "listener" {
  inherits = ["_common"]
  target   = "listener"
  output = [
    "type=image,name=${REGISTRY}/${REPO}/listener,push-by-digest=true,name-canonical=true,push=true,compression=zstd"
  ]
}

target "runner-hook" {
  inherits = ["_common"]
  target   = "runner-hook"
  output = [
    "type=image,name=${REGISTRY}/${REPO}/runner-hook,push-by-digest=true,name-canonical=true,push=true,compression=zstd"
  ]
}

