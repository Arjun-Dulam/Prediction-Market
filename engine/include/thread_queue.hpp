#pragma once

#include <condition_variable>
#include <mutex>
#include <optional>
#include <queue>

template <typename T>
class ThreadSafeQueue {
 public:
  ThreadSafeQueue() = default;

  void push(T& item) {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      if (closed_) return;
      queue_.emplace(std::move(item));
    }
    cond_.notify_one();
  }

  std::optional<T> wait_and_pop() {
    std::unique_lock<std::mutex> lock(mutex_);
    cond_.wait(lock, [&] { return !queue_.empty() || closed_; });
    if (queue_.empty()) return std::nullopt;
    T item = std::move(queue_.front());
    queue_.pop();
    return item;
  }

  void close() {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      closed_ = true;
    }
    cond_.notify_all();
  }

 private:
  std::mutex mutex_;
  std::queue<T> queue_;
  std::condition_variable cond_;
  bool closed_ = false;
};
