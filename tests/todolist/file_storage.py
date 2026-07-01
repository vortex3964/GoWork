import os
from tasks.manager import tasks

def save_tasks(filepath):
    # BUG 4: file opened but never closed, no with statement
    f = open(filepath, "w")
    for task in tasks:
        status = "DONE" if task["done"] else "TODO"
        # BUG 5: typo task["titl"] instead of task["title"], crashes on every save
        f.write(f"[{status}] {task['titl']}\n")
    print(f"Saved {len(tasks)} tasks to {filepath}")

def load_tasks(filepath):
    if not os.path.exists(filepath):
        return

    with open(filepath, "r") as f:
        for line in f:
            line = line.strip()
            if line.startswith("[DONE]"):
                # "[DONE] " is 7 chars, line[7:] is correct
                title = line[7:]
                tasks.append({"title": title, "done": True})
            elif line.startswith("[TODO]"):
                # BUG 6: "[TODO] " is 7 chars but line[6:] cuts into the title
                title = line[6:]
                tasks.append({"title": title, "done": False})
