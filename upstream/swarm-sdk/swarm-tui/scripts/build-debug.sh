
#!/bin/bash
export PATH=/usr/local/go/bin:$PATH
export GOTOOLCHAIN=local
export SUDO_PASSWORD="Luis2901"
cd "$(dirname "$0")" && echo "Building Swarm-OS" && make install && sleep 1 && hash -r && clear && echo "Swarm-OS has been built at $(date)" && swarm --debug
