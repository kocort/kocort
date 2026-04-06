#include "models.h"
#include <cmath>

ggml_cgraph *clip_graph_gemma4v::build()
{
    ggml_tensor *inp_raw = build_inp_raw();

    // Note: input rescaling [0,1] -> [-1,1] is handled in preprocessing
    // via overridden image_mean=0.5, image_std=0.5

    ggml_tensor *inp = ggml_conv_2d(ctx0, model.patch_embeddings_0, inp_raw, patch_size, patch_size, 0, 0, 1, 1);
    inp = ggml_reshape_2d(ctx0, inp, n_patches, n_embd);
    inp = ggml_cont(ctx0, ggml_transpose(ctx0, inp));
    cb(inp, "inp", -1);

    // NaFlex position embeddings via pos_x/pos_y input tensors
    ggml_tensor *pos_x = ggml_new_tensor_1d(ctx0, GGML_TYPE_I32, n_patches);
    ggml_set_name(pos_x, "pos_x");
    ggml_set_input(pos_x);

    ggml_tensor *pos_y = ggml_new_tensor_1d(ctx0, GGML_TYPE_I32, n_patches);
    ggml_set_name(pos_y, "pos_y");
    ggml_set_input(pos_y);

    if (model.position_embeddings)
    {
        const int64_t pos_size = model.position_embeddings->ne[1];
        const size_t nb1 = ggml_row_size(model.position_embeddings->type, n_embd);

        // Position embeddings stored as two lookup tables (x=col, y=row)
        ggml_tensor *tbl_x = ggml_view_2d(ctx0, model.position_embeddings,
                                          n_embd, pos_size, nb1, 0);
        ggml_tensor *tbl_y = ggml_view_2d(ctx0, model.position_embeddings,
                                          n_embd, pos_size, nb1, pos_size * nb1);

        ggml_tensor *emb_x = ggml_get_rows(ctx0, tbl_x, pos_x);
        ggml_tensor *emb_y = ggml_get_rows(ctx0, tbl_y, pos_y);

        inp = ggml_add(ctx0, inp, emb_x);
        inp = ggml_add(ctx0, inp, emb_y);
        cb(inp, "pos_embd", -1);
    }

    // 2D RoPE with NeoX ordering for attention (applied per-layer to Q and K)
    auto add_pos = [&](ggml_tensor *cur, const clip_layer &)
    {
        const int64_t n_dim = cur->ne[0];
        const int64_t cur_n_head = cur->ne[1];
        const int64_t n_pos = cur->ne[2];

        // First half uses pos_x (column positions)
        ggml_tensor *first;
        {
            first = ggml_view_3d(ctx0, cur,
                                 n_dim / 2, cur_n_head, n_pos,
                                 cur->nb[1],
                                 cur->nb[2],
                                 0);
            first = ggml_rope_ext(
                ctx0,
                first,
                pos_x,
                nullptr,
                n_dim / 2,
                GGML_ROPE_TYPE_NEOX, 0, hparams.rope_theta,
                1.0f, 0.0f, 1.0f, 0.0f, 0.0f);
        }

        // Second half uses pos_y (row positions)
        ggml_tensor *second;
        {
            second = ggml_view_3d(ctx0, cur,
                                  n_dim / 2, cur_n_head, n_pos,
                                  cur->nb[1],
                                  cur->nb[2],
                                  n_dim / 2 * ggml_element_size(cur));
            second = ggml_rope_ext(
                ctx0,
                second,
                pos_y,
                nullptr,
                n_dim / 2,
                GGML_ROPE_TYPE_NEOX, 0, hparams.rope_theta,
                1.0f, 0.0f, 1.0f, 0.0f, 0.0f);
        }

        cur = ggml_concat(ctx0, first, second, 0);
        return cur;
    };

    // kq_scale = 1.0 for Gemma4V (no 1/sqrt(d_head) scaling)
    const float gemma4v_kq_scale = 1.0f;

    ggml_tensor *inpL = inp;

    // pre-layernorm
    if (model.pre_ln_w)
    {
        inpL = build_norm(inpL, model.pre_ln_w, model.pre_ln_b, NORM_TYPE_RMS, eps, -1);
        cb(inpL, "pre_ln", -1);
    }

    // loop over layers
    for (int il = 0; il < n_layer; il++)
    {
        auto &layer = model.layers[il];
        ggml_tensor *cur = inpL;

        // layernorm1
        cur = build_norm(cur, layer.ln_1_w, layer.ln_1_b, NORM_TYPE_RMS, eps, il);
        cb(cur, "layer_inp_normed", il);

        // self-attention
        {
            ggml_tensor *Qcur = ggml_mul_mat(ctx0, layer.q_w, cur);
            if (layer.q_b)
                Qcur = ggml_add(ctx0, Qcur, layer.q_b);

            ggml_tensor *Kcur = ggml_mul_mat(ctx0, layer.k_w, cur);
            if (layer.k_b)
                Kcur = ggml_add(ctx0, Kcur, layer.k_b);

            ggml_tensor *Vcur = ggml_mul_mat(ctx0, layer.v_w, cur);
            if (layer.v_b)
                Vcur = ggml_add(ctx0, Vcur, layer.v_b);

            // QK norms (per-head)
            if (layer.q_norm)
            {
                Qcur = build_norm(Qcur, layer.q_norm, NULL, NORM_TYPE_RMS, eps, il);
                cb(Qcur, "Qcur_norm", il);
            }
            if (layer.k_norm)
            {
                Kcur = build_norm(Kcur, layer.k_norm, NULL, NORM_TYPE_RMS, eps, il);
                cb(Kcur, "Kcur_norm", il);
            }

            Qcur = ggml_reshape_3d(ctx0, Qcur, d_head, n_head, n_patches);
            Kcur = ggml_reshape_3d(ctx0, Kcur, d_head, n_head, n_patches);
            Vcur = ggml_reshape_3d(ctx0, Vcur, d_head, n_head, n_patches);

            // 2D RoPE on Q and K
            Qcur = add_pos(Qcur, layer);
            Kcur = add_pos(Kcur, layer);
            cb(Qcur, "Qcur_pos", il);
            cb(Kcur, "Kcur_pos", il);

            // V normalization (Gemma4V specific)
            Vcur = ggml_rms_norm(ctx0, Vcur, eps);
            cb(Vcur, "Vcur_normed", il);

            cur = build_attn(layer.o_w, layer.o_b,
                             Qcur, Kcur, Vcur, nullptr, gemma4v_kq_scale, il);
            cb(cur, "attn_out", il);
        }

        // attention post-norm
        if (layer.attn_post_norm)
        {
            cur = build_norm(cur, layer.attn_post_norm, nullptr, NORM_TYPE_RMS, eps, il);
            cb(cur, "attn_post_normed", il);
        }

        // residual
        cur = ggml_add(ctx0, cur, inpL);
        inpL = cur;

        cb(cur, "ffn_inp", il);

        // layernorm2
        cur = build_norm(cur, layer.ln_2_w, layer.ln_2_b, NORM_TYPE_RMS, eps, il);
        cb(cur, "ffn_inp_normed", il);

        // SwiGLU FFN
        cur = build_ffn(cur,
                        layer.ff_up_w, layer.ff_up_b,
                        layer.ff_gate_w, layer.ff_gate_b,
                        layer.ff_down_w, layer.ff_down_b,
                        hparams.ffn_op, il);
        cb(cur, "ffn_out", il);

        // FFN post-norm
        if (layer.ffn_post_norm)
        {
            cur = build_norm(cur, layer.ffn_post_norm, nullptr, NORM_TYPE_RMS, eps, il);
            cb(cur, "ffn_post_normed", il);
        }

        // residual
        cur = ggml_add(ctx0, inpL, cur);
        cb(cur, "layer_out", il);

        inpL = cur;
    }

    // post-layernorm
    if (model.post_ln_w)
    {
        inpL = build_norm(inpL, model.post_ln_w, model.post_ln_b, NORM_TYPE_RMS, eps, -1);
    }

    // Gemma4VisionPooler: 2D average pooling
    {
        const int kernel_size = hparams.n_merge;
        GGML_ASSERT(kernel_size > 0);

        // [n_embd, n_patches] -> [n_patches_x, n_patches_y, n_embd, 1]
        ggml_tensor *cur = ggml_cont_4d(ctx0, ggml_transpose(ctx0, inpL),
                                        n_patches_x, n_patches_y, n_embd, 1);
        cur = ggml_pool_2d(ctx0, cur, GGML_OP_POOL_AVG,
                           kernel_size, kernel_size, kernel_size, kernel_size, 0, 0);

        const int out_x = n_patches_x / kernel_size;
        const int out_y = n_patches_y / kernel_size;
        // [out_x, out_y, n_embd, 1] -> [n_embd, out_x * out_y]
        cur = ggml_reshape_3d(ctx0, cur, out_x * out_y, n_embd, 1);
        cur = ggml_cont(ctx0, ggml_transpose(ctx0, cur));

        // Scale by sqrt(n_embd)
        cur = ggml_scale(ctx0, cur, sqrtf((float)n_embd));
        cb(cur, "pooled", -1);

        inpL = cur;
    }

    // Gemma4MultimodalEmbedder: projection
    ggml_tensor *cur = ggml_mul_mat(ctx0, model.mm_input_proj_w, inpL);
    cb(cur, "projected", -1);

    // embedding_post_projection_norm (RMSNorm without weight)
    cur = ggml_rms_norm(ctx0, cur, eps);
    cb(cur, "projected_normed", -1);

    ggml_build_forward_expand(gf, cur);
    return gf;
}
