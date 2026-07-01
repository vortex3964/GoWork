#include <stdio.h>
#include "history.h"

void add_to_history(int history[], int *count, int result) {
    // BUG 5: no bounds check, writes past array after 10 entries
    history[*count] = result;
    (*count)++;
}

void print_history(int history[], int count) {
    printf("\n-- Calculation History --\n");
    // BUG 6: i <= count goes one past the last valid index, reads uninitialized memory
    for (int i = 0; i <= count; i++) {
        printf("  result %d: %d\n", i + 1, history[i]);
    }
}
