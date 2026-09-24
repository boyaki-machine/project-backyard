openssl req -x509 -newkey rsa:2048 -sha256 -days 365 -nodes \
  -keyout pb.key -out pb.crt \
  -subj "/CN=pb.example.com" \
  -addext "subjectAltName=DNS:pb.example.com,DNS:localhost" \
  -addext "basicConstraints=critical,CA:FALSE" \
  -addext "extendedKeyUsage=serverAuth"
