/**
 * gitindex — Interactive Animated Canvas Background
 * Implements a premium node-garden (particles network/constellation) matching the Neo-Glow Glassmorphism theme.
 */

(function () {
  const canvas = document.getElementById('node-garden');
  if (!canvas) return;

  const ctx = canvas.getContext('2d');
  let particles = [];
  let mouse = { x: null, y: null, radius: 180 };
  let animationFrameId = null;

  // Track mouse coordinates relative to the viewport
  window.addEventListener('mousemove', (e) => {
    mouse.x = e.clientX;
    mouse.y = e.clientY;
  });

  window.addEventListener('mouseout', () => {
    mouse.x = null;
    mouse.y = null;
  });

  class Particle {
    constructor(w, h) {
      this.x = Math.random() * w;
      this.y = Math.random() * h;
      // Baseline velocity for a slow, premium floating effect
      this.originalVx = (Math.random() - 0.5) * 0.35;
      this.originalVy = (Math.random() - 0.5) * 0.35;
      
      // Ensure we don't have static/inactive particles
      if (Math.abs(this.originalVx) < 0.05) this.originalVx = 0.08 * Math.sign(this.originalVx || 1);
      if (Math.abs(this.originalVy) < 0.05) this.originalVy = 0.08 * Math.sign(this.originalVy || 1);
      
      this.vx = this.originalVx;
      this.vy = this.originalVy;
      this.size = Math.random() * 2 + 1.2;
      this.alpha = Math.random() * 0.45 + 0.25; // Opacity between 0.25 and 0.70
    }

    update(w, h) {
      this.x += this.vx;
      this.y += this.vy;

      // Bounce off boundaries with a small safety margin
      if (this.x < 0) {
        this.x = 0;
        this.vx *= -1;
        this.originalVx *= -1;
      } else if (this.x > w) {
        this.x = w;
        this.vx *= -1;
        this.originalVx *= -1;
      }

      if (this.y < 0) {
        this.y = 0;
        this.vy *= -1;
        this.originalVy *= -1;
      } else if (this.y > h) {
        this.y = h;
        this.vy *= -1;
        this.originalVy *= -1;
      }

      // Mouse repulsion: push particles away smoothly when cursor approaches
      if (mouse.x !== null && mouse.y !== null) {
        const dx = mouse.x - this.x;
        const dy = mouse.y - this.y;
        const distance = Math.sqrt(dx * dx + dy * dy);

        if (distance < mouse.radius && distance > 0) {
          const force = (mouse.radius - distance) / mouse.radius;
          // Normalize direction and apply repulsion force
          this.vx -= (dx / distance) * force * 0.55;
          this.vy -= (dy / distance) * force * 0.55;
        }
      }

      // Gradually restore velocity back to original baseline float speed
      this.vx += (this.originalVx - this.vx) * 0.035;
      this.vy += (this.originalVy - this.vy) * 0.035;
    }

    draw() {
      // Glow effect for nodes using box shadows in 2D context
      ctx.fillStyle = `rgba(255, 75, 110, ${this.alpha})`;
      ctx.shadowBlur = 6;
      ctx.shadowColor = 'rgba(255, 75, 110, 0.5)';
      ctx.beginPath();
      ctx.arc(this.x, this.y, this.size, 0, Math.PI * 2);
      ctx.fill();
      ctx.shadowBlur = 0; // Reset shadow blur immediately
    }
  }

  function init() {
    const w = window.innerWidth;
    const h = window.innerHeight;
    canvas.width = w;
    canvas.height = h;

    // Adjust particle count relative to screen area (responsive density)
    // Max 120 particles to keep drawing CPU cycles light
    const calculatedCount = Math.floor((w * h) / 11000);
    const particleCount = Math.min(Math.max(calculatedCount, 25), 110);

    particles = [];
    for (let i = 0; i < particleCount; i++) {
      particles.push(new Particle(w, h));
    }
  }

  function connect() {
    const w = canvas.width;
    const h = canvas.height;
    const maxDistance = 135;

    for (let i = 0; i < particles.length; i++) {
      const p1 = particles[i];
      for (let j = i + 1; j < particles.length; j++) {
        const p2 = particles[j];
        const dx = p1.x - p2.x;
        const dy = p1.y - p2.y;
        const distance = Math.sqrt(dx * dx + dy * dy);

        if (distance < maxDistance) {
          const alpha = (1 - distance / maxDistance) * 0.12;
          ctx.strokeStyle = `rgba(255, 75, 110, ${alpha})`;
          ctx.lineWidth = 0.85;
          ctx.beginPath();
          ctx.moveTo(p1.x, p1.y);
          ctx.lineTo(p2.x, p2.y);
          ctx.stroke();
        }
      }

      // Connect nodes to the cursor to create interactive constellation branches
      if (mouse.x !== null && mouse.y !== null) {
        const dx = p1.x - mouse.x;
        const dy = p1.y - mouse.y;
        const distance = Math.sqrt(dx * dx + dy * dy);

        if (distance < mouse.radius) {
          const alpha = (1 - distance / mouse.radius) * 0.22;
          ctx.strokeStyle = `rgba(255, 75, 110, ${alpha})`;
          ctx.lineWidth = 0.95;
          ctx.beginPath();
          ctx.moveTo(p1.x, p1.y);
          ctx.lineTo(mouse.x, mouse.y);
          ctx.stroke();
        }
      }
    }
  }

  function animate() {
    // Clear canvas with a transparent layer so it sits cleanly on top of the CSS body gradient
    ctx.clearRect(0, 0, canvas.width, canvas.height);

    const w = canvas.width;
    const h = canvas.height;

    for (let i = 0; i < particles.length; i++) {
      particles[i].update(w, h);
      particles[i].draw();
    }
    connect();

    animationFrameId = requestAnimationFrame(animate);
  }

  // Handle resizing events gracefully
  let resizeTimeout;
  window.addEventListener('resize', () => {
    clearTimeout(resizeTimeout);
    resizeTimeout = setTimeout(() => {
      init();
    }, 150);
  });

  // Start the background animation
  init();
  animate();
})();
