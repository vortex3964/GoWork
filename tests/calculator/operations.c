#include <stdio.h>
#include "operations.h"

// BUG 1: return type is int, should be double for decimal division
// BUG 2: parameters are int, should be double
int calculate(int a, int b, char op) {
    int result = 0;
    switch (op) {
        case '+':
            result = a + b;
            // BUG 3: missing break, falls through into '-'
        case '-':
            result = a - b;
            break;
        case '*':
            result = a * b;
            break;
        case '/':
            // BUG 4: no division by zero check
            result = a / b;
            break;
        default:
            printf("Unknown operator: %c\n", op);
            break;
    }
    return result;
}
