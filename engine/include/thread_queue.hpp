#pragma once

#include <condition_variable>
#include <mutex>
#include <queue>

#include "../include/order.hpp"

class ThreadSafeQueue {
 public:
  ThreadSafeQueue() = default;

  void push(Order item) {
    {
      std::lock_guard<std::mutex> lock(mutex_);
      queue_.push(item);
    }
    cond_.notify_one();
  }

  Order dequeue() {
    Order res;
    {
      std::lock_guard<std::mutex> lock(mutex_);
      res = queue_.front();
      queue_.pop();
    }
    return res;
  }

 private:
  std::mutex mutex_;
  std::queue<Order> queue_;
  std::condition_variable cond_;
};
