from tasks.manager import add_task, show_tasks, complete_task, delete_task
from storage.file_storage import save_tasks, load_tasks

FILEPATH = "tasks.txt"

def main():
    load_tasks(FILEPATH)
    print("=== Todo List ===")

    while True:
        print("\n1. Add task")
        print("2. Show tasks")
        print("3. Complete task")
        print("4. Delete task")
        print("5. Save and quit")

        choice = input("\nChoice: ").strip()

        if choice == "1":
            title = input("Task title: ").strip()
            if title:
                add_task(title)

        elif choice == "2":
            show_tasks()

        elif choice == "3":
            show_tasks()
            # BUG 7: no try/except, crashes with ValueError on non-numeric input
            index = int(input("Enter task number to complete: "))
            complete_task(index)

        elif choice == "4":
            show_tasks()
            # BUG 7 (same): no try/except here either
            index = int(input("Enter task number to delete: "))
            delete_task(index)

        elif choice == "5":
            save_tasks(FILEPATH)
            break

        else:
            print("Invalid choice.")

if __name__ == "__main__":
    main()
