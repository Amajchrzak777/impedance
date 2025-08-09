# goimpcore

## Usage

```bash
./goeiscircuit -c "(RC)" -v 100 -v 0.001 -f 1000 -f 100 -f 10 -fauto=0 -imgsave
./goeiscircuit -c "(RC)" -v 100 -v 0.001 -imgsave -imgout | display
./goeiscircuit -c "(RC)W" -v 100 -v 0.0001 -v 0.01  -imgout | display
./goeiscircuit -c "R(RC)O" -v 100 -v 100 -v 0.00001 -v 0.01 -v 10 -fauto=0 -f 56200

./goimpsolver -c "R(CR)" -f ASTM\ dummy\ cell\,\ measured.txt -v 13 -v 0.0000667 -v 13 -imgout | display
./goimpsolver -c "R(Q(R(QR)))" -f Cu_Ni\ exposed\ to\ sea\ water.txt -v 11.46 -v 0.0000667 -v 0.6515 -v 230.4 -v 0.00072 -v 0.6104 -v 2053 -imgout | display
./goimpsolver -c "R(Q(R(QR)))" -f Cu_Ni\ exposed\ to\ sea\ water.txt -v 11.46 -v 0.0000667 -v 0.6515 -v 230.4 -v 0.00072 -v 0.6104 -v 2053 -imgout -b 1 -e 8 | display

# go get private repos
git config --global url."git@github.com:".insteadOf "https://github.com/"

go build -o clib.so -buildmode=c-shared clib.go
cd lib
gcc -o test test.c ./clib.so
```

## Resources
https://www.bio-logic.net/wp-content/uploads/Zdiffusion.pdf
