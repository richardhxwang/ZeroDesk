FROM debian:trixie
RUN apt-get update -o Acquire::Retries=3 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends zsh git ca-certificates sudo procps python3 less && rm -rf /var/lib/apt/lists/*
RUN useradd -m -s /bin/bash -G sudo pi && echo 'pi ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/pi
COPY zerodesk /usr/local/bin/zerodesk
USER pi
WORKDIR /home/pi
RUN mkdir -p Music Pictures Recordings Documents Downloads Projects/weather-station && cd Projects/weather-station \
 && git init -q -b main && git config user.name pi && git config user.email pi@cardputerzero \
 && printf 'import time\n\ndef read_temp():\n    with open("/sys/bus/iio/devices/iio:device0/in_temp_input") as f:\n        return int(f.read()) / 1000\n\nwhile True:\n    print(f"{read_temp():.1f} C")\n    time.sleep(5)\n' > main.py \
 && printf '# Weather station\n\nReads the temperature every five seconds.\n' > README.md && git add . && git commit -qm "Read the temperature" 
ENV LANG=C.UTF-8
CMD ["/usr/local/bin/zerodesk", "-addr", ":8686", "-also", "", "-no-auth"]
