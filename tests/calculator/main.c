#include <stdio.h>
#include "operations/operations.h"
#include "history/history.h"

int main() {
    int a, b;
    char op;
    int history[MAX_HISTORY];
    int history_count = 0;

    printf("Simple Calculator (Ctrl+D to quit)\n");

    while (1) {
        printf("\nEnter expression (e.g. 5 + 3): ");

        if (scanf("%d %c %d", &a, &op, &b) != 3) {
            break;
        }

        int result = calculate(a, b, op);
        printf("Result: %d\n", result);

        add_to_history(history, &history_count, result);

        if (history_count > 1) {
            print_history(history, history_count);
        }
    }

    return 0;
}
