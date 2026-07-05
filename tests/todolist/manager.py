from tasks.task import new_task

tasks = []

def add_task(title):
    tasks.append(new_task(title))
    print(f"Added: '{title}'")

def show_tasks():
    if not tasks:
        print("No tasks yet!")
        return

    print("\n--- Tasks ---")
    # BUG 1: range starts at 1, skips the first task at index 0
    for i in range(1, len(tasks)):
        status = "x" if tasks[i]["done"] else " "
        print(f"  {i}. [{status}] {tasks[i]['title']}")

def complete_task(index):
    # BUG 2: user sees 1-based numbers but index is used directly (0-based)
    # task 1 completes task at index 1 (the second task), task 0 is unreachable
    tasks[index]["done"] = True
    print(f"Task {index} marked complete!")

def delete_task(index):
    # BUG 3: same 1-based vs 0-based problem as complete_task
    removed = tasks.pop(index)
    print(f"Deleted: '{removed['title']}'")
