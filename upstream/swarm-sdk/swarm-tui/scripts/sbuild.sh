#!/bin/bash
make install | xargs -I {} swarm -p 'please fix this build issue understand the context of the changes that happened around it before you do {}'
