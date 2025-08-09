#include <stdio.h>
#include "clib.h"

int main() {
    GoString code = {"(RC)", 4};

    GoInt data[2] = {100, 0.001};
    GoSlice values = {data, 2, 2};
    GoInt dataVal[22] = {1000000, 500000, 200000, 100000, 50000, 20000, 10000, 5000, 2000, 1000, 500, 200, 100, 50, 20, 10, 5, 2, 1, 0.5, 0.2, 0.1};
    GoSlice freqs = {dataVal, 22, 22};
    GoSlice res = {};
    res = Calculate(code, values, freqs);


//    for (int i = 0; i < 6; i++){
//        printf("%d,", ((GoComplex128 *)res)[i]);
//    }
}