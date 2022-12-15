# Docker deployment

### Prerequisites 
* [Docker Desktop](https://www.docker.com/products/docker-desktop/) - Docker 17.12.0+
* [Docker compose 2+](https://github.com/docker/compose/releases/tag/v2.14.1)
* [Make](https://www.gnu.org/software/make/) - if using `make`

## Local development
Running `polygon-edge` local cluster with docker can be done very easily by using provided `Makefile`
or by running `docker-compose` manually.

### Using `make`
***All commands need to be run from the repo root / root folder.***

#### `ibft` consensus
* `make run-local` - deploy environment
* `make stop-local` - stop containers
* `make destroy-local` - destroy environment (delete containers and volumes)

#### `polybft` consensus
* `make run-local-polybft` - deploy environment
* `make stop-local-polybft` - stop containers
* `make destroy-local-polybft` - destroy environment (delete containers and volumes)

### Using `docker-compose`
***All commands need to be run from the repo root / root folder.***

### `ibft` consensus
* `export EDGE_CONSENSUS=ibft` - set consensus
* `docker-compose -f ./docker/local/docker-compose.yml up -d --build` - deploy environment
* `docker-compose -f ./docker/local/docker-compose.yml stop` - stop containers
* `docker-compose -f ./docker/local/docker-compose.yml down -v` - destroy environment

### `polybft` consensus
* `export EDGE_CONSENSUS=polybft` - set consensus
* `docker-compose -f ./docker/local/docker-compose.yml up -d --build` - deploy environment
* `docker-compose -f ./docker/local/docker-compose.yml stop` - stop containers
* `docker-compose -f ./docker/local/docker-compose.yml down -v` - destroy environment

## Considerations

### Core-contracts for `polybft` consensus
As `polybft` consensus requires a set of compiled smart contracts, they are downloaded from 
[core-contracts](https://github.com/0xPolygon/core-contracts) `dev` branch during containers build phase.  
When rebuilding containers, these contracts will remain in docker build cache, meaning that they won't be 
updated everytime when rebuilding containers.   
If there is a need to pull down the latest changes from `core-contracts` repo `dev` branch, docker build 
cache needs to be purged.

#### Purge docker cache
`docker builder prune --all` - delete all build cache

### Build times
When building containers for the first time (or after purging docker build cache)
it might take a while to complete, depending on the hardware that the build operation is running on.
Subsequent builds will take much less time to complete. 
